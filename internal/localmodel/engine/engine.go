package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/kocort/kocort/internal/localmodel/catalog"
	"github.com/kocort/kocort/internal/localmodel/chatfmt"
	"github.com/kocort/kocort/internal/localmodel/ffi"
)

// Engine is the core inference engine that owns the llama.cpp model and context.
// It manages parallel sequences, batch processing, and token generation.
type Engine struct {
	cfg EngineConfig

	// purego library handle
	lib *ffi.Library

	// model arch from GGUF metadata
	modelArch string

	// chat format (render + parser)
	format chatfmt.Format

	// loaded model and context
	model *ffi.Model
	ctx   *ffi.Context
	image *ffi.MtmdContext

	// KV cache
	cache *kvCache

	// parallel sequence management
	mu      sync.Mutex
	cond    *sync.Cond
	seqs    []*sequence
	seqsSem *semaphore.Weighted
	nextSeq int

	// lifecycle
	status   EngineStatus
	progress float32
	ready    sync.WaitGroup

	// global thinking default
	enableThinking bool
}

// NewEngine creates and initializes a new inference engine.
// It loads the model and creates the llama context. The decode loop is NOT
// started — call Run(ctx) to begin processing.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	ffi.BackendInit()
	lib := ffi.DefaultLibrary()
	if lib == nil {
		return nil, fmt.Errorf("llamadl backend not available: %v", ffi.LibraryError())
	}

	// Update runtime capabilities based on actual library features.
	catalog.SetRuntimeCapabilities(catalog.Capabilities{
		Vision:    lib.IsMtmdAvailable(),
		Tools:     true,
		Reasoning: true,
		Coding:    true,
	})

	e := &Engine{
		cfg:            cfg,
		lib:            lib,
		status:         StatusCreated,
		enableThinking: cfg.EnableThinking,
	}
	e.cond = sync.NewCond(&e.mu)
	e.ready.Add(1)

	if err := e.Load(); err != nil {
		return nil, err
	}

	return e, nil
}

// Status returns the engine's current lifecycle status.
func (e *Engine) Status() EngineStatus { return e.status }

// Progress returns the model loading progress [0, 1].
func (e *Engine) Progress() float32 { return e.progress }

// ContextSize returns the effective per-slot context length.
func (e *Engine) ContextSize() int {
	if e.cache == nil {
		return 0
	}
	return e.cache.ctxLen
}

// EnableThinking returns whether thinking is globally enabled.
func (e *Engine) EnableThinking() bool { return e.enableThinking }

// HasVision returns true if the multimodal projector was loaded successfully.
func (e *Engine) HasVision() bool { return e.image != nil }

// SetEnableThinking sets the global thinking default.
func (e *Engine) SetEnableThinking(v bool) { e.enableThinking = v }

// ModelArch returns the GGUF general.architecture value.
func (e *Engine) ModelArch() string { return e.modelArch }

// ModelDesc returns a human-readable model description (e.g. "7B Q4_0").
func (e *Engine) ModelDesc() string {
	if e.model == nil {
		return ""
	}
	return e.model.Desc()
}

// ModelSize returns the total model size in bytes.
func (e *Engine) ModelSize() uint64 {
	if e.model == nil {
		return 0
	}
	return e.model.Size()
}

// ModelNParams returns the number of model parameters.
func (e *Engine) ModelNParams() uint64 {
	if e.model == nil {
		return 0
	}
	return e.model.NParams()
}

// ModelChatTemplate returns the chat template string embedded in the model.
func (e *Engine) ModelChatTemplate() string {
	if e.model == nil {
		return ""
	}
	return e.model.ChatTemplate("")
}

// Load initializes the model, context, and cache.
func (e *Engine) Load() error {
	if e.status == StatusReady {
		return nil
	}
	e.status = StatusLoading

	// Resolve defaults.
	batchSize := e.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 512
	}
	parallel := e.cfg.Parallel
	if parallel <= 0 {
		parallel = 1
	}
	kvSize := e.cfg.ContextSize
	if kvSize <= 0 {
		kvSize = 2048
	}
	threads := e.cfg.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	gpuLayers := e.cfg.GPULayers
	if gpuLayers < 0 {
		gpuLayers = 999 // offload all
	}

	// Load model.
	mainGPU := e.cfg.MainGPU
	if mainGPU == 0 {
		mainGPU = -1 // default: let the library choose
	}
	modelParams := ffi.ModelParams{
		NumGpuLayers: gpuLayers,
		MainGpu:      mainGPU,
		UseMmap:      e.cfg.UseMmap,
		Progress: func(p float32) {
			e.progress = p
		},
	}

	model, err := ffi.LoadModelFromFile(e.lib, e.cfg.ModelPath, modelParams)
	if err != nil {
		e.status = StatusCreated
		return fmt.Errorf("load model: %w", err)
	}
	e.model = model

	// Read model arch from loaded model metadata (replaces separate GGUF parsing).
	if arch := model.MetaValStr("general.architecture"); arch != "" {
		e.modelArch = arch
	}

	// Detect the chat format from the model's Jinja2 chat template and architecture.
	e.format = chatfmt.Detect(model.ChatTemplate(""), e.modelArch)

	// Create context.
	fa := ffi.FlashAttentionType(e.cfg.FlashAttention)
	ctxParams := ffi.NewContextParams(e.lib, kvSize, batchSize, parallel, threads, fa, e.cfg.KVCacheType)
	lc, err := ffi.NewContextWithModel(e.lib, model, ctxParams)
	if err != nil {
		ffi.FreeModel(model)
		e.model = nil
		e.status = StatusCreated
		return fmt.Errorf("create context: %w", err)
	}
	e.ctx = lc

	// Create cache.
	cache, err := newKVCache(lc, kvSize, parallel)
	if err != nil {
		lc.Free()
		ffi.FreeModel(model)
		e.model = nil
		e.ctx = nil
		e.status = StatusCreated
		return fmt.Errorf("create cache: %w", err)
	}
	e.cache = cache

	// Load multimodal projector if configured.
	if e.cfg.MmprojPath != "" && e.lib.IsMtmdAvailable() {
		mtmd, err := ffi.NewMtmdContext(e.lib, lc, e.cfg.MmprojPath)
		if err != nil {
			slog.Warn("[engine] failed to load mmproj, vision disabled",
				"path", e.cfg.MmprojPath, "error", err)
		} else {
			e.image = mtmd
			slog.Info("[engine] multimodal projector loaded", "path", e.cfg.MmprojPath)
		}
	} else if e.cfg.MmprojPath == "" {
		slog.Info("[engine] no mmproj path configured, vision disabled")
	} else {
		slog.Warn("[engine] mtmd library not available, vision disabled",
			"mmprojPath", e.cfg.MmprojPath, "mtmdAvailable", e.lib.IsMtmdAvailable())
	}

	// Initialize sequence management.
	e.seqs = make([]*sequence, parallel)
	e.seqsSem = semaphore.NewWeighted(int64(parallel))

	e.status = StatusReady
	e.ready.Done()

	slog.Info("[engine] model loaded",
		"path", e.cfg.ModelPath,
		"arch", e.modelArch,
		"desc", model.Desc(),
		"params", model.NParams(),
		"layers", model.NLayer(),
		"size_bytes", model.Size(),
		"ctx", kvSize,
		"batch", batchSize,
		"parallel", parallel,
		"threads", threads,
		"gpu_layers", gpuLayers,
		"hasVision", e.image != nil)

	return nil
}

// Run starts the main decode loop. It blocks until ctx is cancelled.
// Panics from the Go layer are caught and logged to prevent
// the entire process from crashing.
func (e *Engine) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[engine] Run panicked — recovered", "panic", r)
		}
	}()

	e.ready.Wait()
	if e.ctx != nil {
		e.ctx.ResetAbort()
	}

	tokenBatch, err := ffi.NewBatch(e.lib, e.cfg.batchSize(), len(e.seqs), 0)
	if err != nil {
		slog.Error("[engine] failed to create token batch", "error", err)
		return
	}
	defer tokenBatch.Free()

	var embedBatch *ffi.Batch
	if e.image != nil {
		embedBatch, err = ffi.NewBatch(e.lib, e.cfg.batchSize(), len(e.seqs), e.ctx.Model().NEmbd())
		if err != nil {
			slog.Error("[engine] failed to create embed batch", "error", err)
			return
		}
		defer embedBatch.Free()
	}
	if embedBatch == nil {
		embedBatch = &ffi.Batch{}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			err := e.processBatch(tokenBatch, embedBatch)
			if err != nil {
				if errors.Is(err, ffi.ErrDecodeAborted) {
					if ctx.Err() != nil {
						return // engine-level shutdown
					}
					// Per-request cancellation: remove only the cancelled sequences,
					// reset the abort flag, and let surviving sequences continue.
					slog.Info("[engine] decode aborted by request cancellation, cleaning up")
					e.mu.Lock()
					for i, seq := range e.seqs {
						if seq != nil {
							select {
							case <-seq.quit:
								e.removeSeq(i, DoneDisconnect)
							default:
							}
						}
					}
					e.mu.Unlock()
					e.ctx.ResetAbort()
				} else {
					slog.Error("[engine] processBatch error", "error", err)
					e.mu.Lock()
					for i, seq := range e.seqs {
						if seq != nil {
							e.removeSeq(i, DoneDisconnect)
						}
					}
					e.mu.Unlock()
				}
			}
			tokenBatch.Clear()
			embedBatch.Clear()
		}
	}
}

// cancelSeq signals a single sequence to stop and asks llama.cpp to abort
// any in-flight decode so the engine loop can reclaim it promptly.
// Safe to call multiple times for the same sequence.
func (e *Engine) cancelSeq(seq *sequence) {
	select {
	case <-seq.quit:
		// already closed
	default:
		close(seq.quit)
	}
	if e.ctx != nil {
		e.ctx.RequestAbort()
	}
}

// RequestStop asks llama.cpp to abort any in-flight decode work.
func (e *Engine) RequestStop() {
	if e.ctx != nil {
		e.ctx.RequestAbort()
	}
}

// Close releases all resources held by the engine.
// Panics from the Go layer are caught and logged to prevent
// the entire process from crashing during teardown.
func (e *Engine) Close() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[engine] Close panicked — recovered", "panic", r)
		}
	}()

	e.status = StatusClosed
	if e.ctx != nil {
		e.ctx.PerfPrint()
	}
	if e.image != nil {
		e.image.Free()
		e.image = nil
	}
	if e.ctx != nil {
		e.ctx.Free()
		e.ctx = nil
	}
	if e.model != nil {
		ffi.FreeModel(e.model)
		e.model = nil
	}
}

// batchSize returns the configured batch size with a default.
func (c EngineConfig) batchSize() int {
	if c.BatchSize <= 0 {
		return 512
	}
	return c.BatchSize
}

// ── Sequence creation ────────────────────────────────────────────────────────

// newSequence creates a new inference sequence from a prompt string and images.
func (e *Engine) newSequence(prompt string, images []ImageData, params seqParams) (*sequence, error) {
	e.ready.Wait()

	inputs, err := e.tokenize(prompt, images)
	if err != nil {
		return nil, fmt.Errorf("tokenize: %w", err)
	}
	if len(inputs) == 0 {
		return nil, errors.New("empty input")
	}

	// Diagnostic: count token vs embed inputs.
	tokenInputs, embedInputs := 0, 0
	for _, inp := range inputs {
		if inp.embed != nil {
			embedInputs++
		} else {
			tokenInputs++
		}
	}
	slog.Info("[engine] newSequence inputs",
		"total", len(inputs),
		"tokenInputs", tokenInputs,
		"embedInputs", embedInputs,
		"imageCount", len(images),
		"hasVision", e.image != nil,
		"ctxLen", e.cache.ctxLen)

	numKeep := params.NumKeep
	if numKeep < 0 {
		numKeep = len(inputs)
	}
	if e.model.AddBOSToken() {
		numKeep++
	}
	if numKeep >= e.cache.ctxLen {
		numKeep = e.cache.ctxLen - 1
	}

	// Truncate if necessary.
	if len(inputs) > e.cache.ctxLen {
		if !params.Truncate {
			return nil, errors.New("input exceeds context length")
		}
		discard := len(inputs) - e.cache.ctxLen
		newInputs := make([]input, 0, e.cache.ctxLen)
		newInputs = append(newInputs, inputs[:numKeep]...)
		newInputs = append(newInputs, inputs[numKeep+discard:]...)
		// Recount after truncation.
		truncTokens, truncEmbeds := 0, 0
		for _, inp := range newInputs {
			if inp.embed != nil {
				truncEmbeds++
			} else {
				truncTokens++
			}
		}
		slog.Warn("[engine] truncating prompt",
			"limit", e.cache.ctxLen,
			"original", len(inputs), "new", len(newInputs),
			"origTokens", tokenInputs, "origEmbeds", embedInputs,
			"truncTokens", truncTokens, "truncEmbeds", truncEmbeds)
		inputs = newInputs
	}

	// Create sampling context.
	var sc *ffi.SamplingContext
	if params.Sampling != nil {
		sc, err = ffi.NewSamplingContext(e.lib, e.model, *params.Sampling)
		if err != nil {
			return nil, err
		}
		for _, inp := range inputs {
			if inp.embed == nil {
				sc.Accept(inp.token, false)
			}
		}
	}

	return &sequence{
		inputs:           inputs,
		numPromptInput:   len(inputs),
		numPredict:       params.NumPredict,
		pendingResponses: make([]string, 0),
		responses:        make(chan fragment, 100),
		quit:             make(chan struct{}, 1),
		embedding:        make(chan []float32, 1),
		sampler:          sc,
		embeddingOnly:    params.Embedding,
		stop:             params.Stop,
		numKeep:          numKeep,
		shift:            params.Shift,
		logprobs:         params.Logprobs,
		topLogprobs:      params.TopLogprobs,
	}, nil
}

// tokenize converts a prompt string (with optional image tags) into inputs.
func (e *Engine) tokenize(prompt string, images []ImageData) ([]input, error) {
	var inputs []input
	var parts []string
	var matches [][]string

	if e.image != nil {
		re := regexp.MustCompile(`\[img-(\d+)\]`)
		parts = re.Split(prompt, -1)
		matches = re.FindAllStringSubmatch(prompt, -1)
	} else {
		if strings.Contains(prompt, "[img-") {
			slog.Warn("[engine] prompt contains image placeholders but vision is not available — images will be sent as literal text",
				"hasVision", false, "imageCount", len(images))
		}
		parts = []string{prompt}
	}

	for i, part := range parts {
		tokens, err := e.ctx.Model().Tokenize(part, i == 0, true)
		if err != nil {
			return nil, err
		}
		for _, t := range tokens {
			inputs = append(inputs, input{token: t})
		}

		if i < len(matches) {
			n, _ := strconv.Atoi(matches[i][1])
			imgIdx := -1
			for j := range images {
				if images[j].ID == n {
					imgIdx = j
					break
				}
			}
			if imgIdx < 0 {
				return nil, fmt.Errorf("invalid image index: %d", n)
			}
			if e.image != nil {
				chunks, err := e.image.MultimodalTokenize(e.ctx, images[imgIdx].Data)
				if err != nil {
					return nil, err
				}
				for _, c := range chunks {
					if len(c.Embed) != 0 {
						inputs = append(inputs, input{embed: c.Embed})
					} else {
						for _, t := range c.Tokens {
							inputs = append(inputs, input{token: t})
						}
					}
				}
			}
		}
	}

	return inputs, nil
}

// Tokenize exposes tokenization for external use.
func (e *Engine) Tokenize(text string) ([]int, error) {
	e.ready.Wait()
	return e.ctx.Model().Tokenize(text, false, true)
}

// ── Batch processing ─────────────────────────────────────────────────────────

// allNil returns true if there are no active sequences.
func (e *Engine) allNil() bool {
	for _, s := range e.seqs {
		if s != nil {
			return false
		}
	}
	return true
}

// removeSeq finishes a sequence: flushes pending output, closes channels, frees resources.
func (e *Engine) removeSeq(idx int, reason DoneReason) {
	seq := e.seqs[idx]
	seq.flush()
	seq.doneReason = reason
	close(seq.responses)
	close(seq.embedding)
	if seq.sampler != nil {
		seq.sampler.Free()
		seq.sampler = nil
	}
	e.cache.release(seq.slot)
	e.seqs[idx] = nil
	e.seqsSem.Release(1)
}

// processBatch is the heart of inference: collects inputs, decodes, samples, and dispatches.
func (e *Engine) processBatch(tokenBatch, embedBatch *ffi.Batch) error {
	e.mu.Lock()
	for e.allNil() {
		e.cond.Wait()
	}
	defer e.mu.Unlock()

	var batch *ffi.Batch
	var numOutputs int

	seqIdx := e.nextSeq - 1
	for range e.seqs {
		seqIdx = (seqIdx + 1) % len(e.seqs)
		seq := e.seqs[seqIdx]
		if seq == nil {
			continue
		}

		// Prediction limit check.
		if seq.numPredict > 0 && seq.numPredicted >= seq.numPredict {
			e.removeSeq(seqIdx, DoneLength)
			continue
		}

		for i, inp := range seq.inputs {
			if len(seq.slot.inputs)+len(seq.pendingInputs)+1 > e.cache.ctxLen {
				if len(seq.pendingInputs) == 0 {
					if !seq.shift {
						e.removeSeq(seqIdx, DoneLength)
						break
					}
					if err := e.cache.shift(seq.slot, seq.numKeep); err != nil {
						return err
					}
				} else {
					break
				}
			}

			isEmbed := inp.embed != nil

			if batch == nil {
				if !isEmbed {
					batch = tokenBatch
				} else {
					batch = embedBatch
				}
			} else if isEmbed != batch.IsEmbedding() {
				e.nextSeq = seqIdx
				break
			}

			if i >= batch.Size() {
				break
			}

			isOutput := i+1 == len(seq.inputs)
			batch.Add(inp.token, inp.embed, len(seq.slot.inputs)+len(seq.pendingInputs), isOutput, seq.slot.id)
			if isOutput {
				numOutputs++
			}

			seq.pendingInputs = append(seq.pendingInputs, inp)
			seq.iBatch = batch.NumTokens() - 1
		}

		seq.inputs = seq.inputs[len(seq.pendingInputs):]
	}

	if batch == nil || batch.NumTokens() == 0 {
		return nil
	}

	t := time.Now()
	if err := e.ctx.Decode(batch); err != nil {
		// Decode did not complete — restore pending inputs so surviving
		// sequences can re-submit them on the next iteration.
		for _, seq := range e.seqs {
			if seq != nil && len(seq.pendingInputs) > 0 {
				seq.inputs = append(seq.pendingInputs, seq.inputs...)
				seq.pendingInputs = nil
			}
		}
		return fmt.Errorf("decode: %w", err)
	}

	if numOutputs > 0 {
		e.ctx.Synchronize()
	}

	for i, seq := range e.seqs {
		if seq == nil {
			continue
		}

		// Commit pending inputs.
		if len(seq.pendingInputs) > 0 {
			seq.slot.inputs = append(seq.slot.inputs, seq.pendingInputs...)
			seq.pendingInputs = nil
		}

		// Still processing prompt, not sampling yet.
		if len(seq.inputs) != 0 {
			seq.promptDuration += time.Since(t)
			continue
		}

		seq.numDecoded++
		if seq.numDecoded > 1 {
			seq.genDuration += time.Since(t)
		} else {
			seq.promptDuration += time.Since(t)
		}

		// Embedding mode.
		if seq.embeddingOnly {
			embed := e.ctx.GetEmbeddingsSeq(seq.slot.id)
			if embed == nil {
				embed = e.ctx.GetEmbeddingsIth(seq.iBatch)
			}
			seq.embedding <- embed
			e.removeSeq(i, DoneStop)
			continue
		}

		// Sample next token.
		token := seq.sampler.Sample(e.ctx, seq.iBatch)
		seq.sampler.Accept(token, true)
		piece := e.model.TokenToPiece(token)

		seq.numPredicted++

		// EOS check.
		if e.model.TokenIsEog(token) {
			e.removeSeq(i, DoneStop)
			continue
		}

		// Compute logprobs if requested.
		if seq.logprobs {
			logits := e.ctx.GetLogitsIthDirect(seq.iBatch)
			if logits != nil {
				lps := computeLogprobs(logits, token, seq.topLogprobs, e.model)
				seq.pendingLogprobs = append(seq.pendingLogprobs, lps...)
			}
		}

		seq.inputs = []input{{token: token}}
		seq.pendingResponses = append(seq.pendingResponses, piece)
		accumulated := strings.Join(seq.pendingResponses, "")

		// Stop sequence check.
		if ok, stop := matchStop(accumulated, seq.stop); ok {
			origLen := len(seq.pendingResponses)
			var tokenTruncated bool
			seq.pendingResponses, tokenTruncated = trimStop(seq.pendingResponses, stop)
			newLen := len(seq.pendingResponses)

			if seq.logprobs {
				removed := origLen - newLen
				newLogLen := len(seq.pendingLogprobs) - removed
				if newLogLen < 0 {
					newLogLen = 0
				}
				seq.pendingLogprobs = seq.pendingLogprobs[:newLogLen]
			}

			// Keep cache inputs aligned.
			cacheLen := len(seq.slot.inputs) + 1
			cacheLen -= origLen - newLen
			if tokenTruncated || origLen == newLen {
				cacheLen--
			}
			if cacheLen < 0 {
				cacheLen = 0
			}
			if cacheLen < len(seq.slot.inputs) {
				seq.slot.inputs = seq.slot.inputs[:cacheLen]
			}

			e.removeSeq(i, DoneStop)
			continue
		}

		if hasStopSuffix(accumulated, seq.stop) {
			continue
		}
		if hasIncompleteUTF8(accumulated) {
			continue
		}

		if !seq.flush() {
			e.removeSeq(i, DoneDisconnect)
		}
	}

	return nil
}

// ── Logprob computation ──────────────────────────────────────────────────────

// computeLogprobs computes log probabilities via numerically stable log-softmax.
func computeLogprobs(logits []float32, selectedToken int, topK int, model *ffi.Model) []LogprobEntry {
	if len(logits) == 0 {
		return nil
	}

	maxLogit := logits[0]
	for _, l := range logits[1:] {
		if l > maxLogit {
			maxLogit = l
		}
	}

	var sumExp float64
	for _, l := range logits {
		sumExp += math.Exp(float64(l - maxLogit))
	}
	logSumExp := float32(math.Log(sumExp))

	logProbs := make([]float32, len(logits))
	for i, l := range logits {
		logProbs[i] = (l - maxLogit) - logSumExp
	}

	entry := LogprobEntry{
		TokenLogprob: TokenLogprob{
			Token:   model.TokenToPiece(selectedToken),
			Logprob: float64(logProbs[selectedToken]),
		},
	}

	if topK > 0 {
		type pair struct {
			id int
			lp float32
		}
		pairs := make([]pair, len(logProbs))
		for i, lp := range logProbs {
			pairs[i] = pair{id: i, lp: lp}
		}
		sort.Slice(pairs, func(a, b int) bool {
			return pairs[a].lp > pairs[b].lp
		})
		k := topK
		if k > len(pairs) {
			k = len(pairs)
		}
		top := make([]TokenLogprob, k)
		for j := 0; j < k; j++ {
			top[j] = TokenLogprob{
				Token:   model.TokenToPiece(pairs[j].id),
				Logprob: float64(pairs[j].lp),
			}
		}
		entry.TopLogprobs = top
	}

	return []LogprobEntry{entry}
}

// ── Sequence placement ───────────────────────────────────────────────────────

// placeSequence acquires a slot and places seq into the engine's sequence list.
func (e *Engine) placeSequence(ctx context.Context, seq *sequence) error {
	if err := e.seqsSem.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("acquire slot: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for i, existing := range e.seqs {
		if existing == nil {
			slot, remaining, err := e.cache.acquire(seq.inputs)
			if err != nil {
				e.seqsSem.Release(1)
				return fmt.Errorf("acquire cache: %w", err)
			}
			seq.slot = slot
			seq.inputs = remaining
			e.seqs[i] = seq
			e.cond.Signal()
			return nil
		}
	}

	e.seqsSem.Release(1)
	return errors.New("no available sequence slot")
}
