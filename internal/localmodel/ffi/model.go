package ffi

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

// FlashAttentionType controls flash attention behavior.
type FlashAttentionType int32

const (
	FlashAttentionAuto     FlashAttentionType = -1
	FlashAttentionDisabled FlashAttentionType = 0
	FlashAttentionEnabled  FlashAttentionType = 1
)

// ModelParams contains parameters for model loading (Go-friendly).
// MainGpu defaults to -1 meaning "use library default".
type ModelParams struct {
	Devices      []uint64
	NumGpuLayers int
	MainGpu      int // -1 = library default, 0+ = specific GPU index
	UseMmap      bool
	TensorSplit  []float32
	Progress     func(float32)
	VocabOnly    bool
}

// Model wraps a llama_model pointer.
type Model struct {
	ptr   uintptr // *llama_model
	lib   *Library
	vocab uintptr // cached *llama_vocab
}

// NumVocab returns the number of tokens in the model vocabulary.
func (m *Model) NumVocab() int {
	return int(m.lib.fnLlamaVocabNTokens(m.Vocab()))
}

// TokenIsEog returns whether the given token is end-of-generation.
func (m *Model) TokenIsEog(token int) bool {
	return m.lib.fnLlamaVocabIsEog(m.Vocab(), int32(token))
}

// AddBOSToken returns whether the model expects a BOS token to be added.
func (m *Model) AddBOSToken() bool {
	return m.lib.fnLlamaVocabGetAddBos(m.Vocab())
}

// TokenToPiece converts a token ID to its string representation.
func (m *Model) TokenToPiece(token int) string {
	tokenLen := int32(12)
	buf := make([]byte, tokenLen)
	n := m.lib.fnLlamaTokenToPiece(
		m.Vocab(),
		int32(token),
		&buf[0],
		tokenLen,
		0,    // lstrip
		true, // special
	)
	if n < 0 {
		tokenLen = -n
		buf = make([]byte, tokenLen)
		m.lib.fnLlamaTokenToPiece(
			m.Vocab(),
			int32(token),
			&buf[0],
			tokenLen,
			0,
			true,
		)
	} else {
		tokenLen = n
	}
	return strings.TrimRight(string(buf[:tokenLen]), "\x00")
}

// Tokenize converts text to tokens.
func (m *Model) Tokenize(text string, addSpecial bool, parseSpecial bool) ([]int, error) {
	maxTokens := int32(len(text) + 2)
	cTokens := make([]int32, maxTokens)
	textPtr, textBuf := cstr(text)

	result := m.lib.fnLlamaTokenize(
		m.Vocab(),
		textPtr,
		int32(len(text)),
		&cTokens[0],
		maxTokens,
		addSpecial,
		parseSpecial,
	)
	runtime.KeepAlive(textBuf)

	// If negative, reallocate with correct size
	if result < 0 {
		maxTokens = -result
		cTokens = make([]int32, maxTokens)
		result = m.lib.fnLlamaTokenize(
			m.Vocab(),
			textPtr,
			int32(len(text)),
			&cTokens[0],
			maxTokens,
			addSpecial,
			parseSpecial,
		)
		runtime.KeepAlive(textBuf)
		if result < 0 {
			return nil, fmt.Errorf("%w: required %d tokens", ErrTokenize, -result)
		}
	}

	tokens := make([]int, result)
	for i := int32(0); i < result; i++ {
		tokens[i] = int(cTokens[i])
	}
	return tokens, nil
}

// NEmbd returns the embedding dimension of the model.
func (m *Model) NEmbd() int {
	return int(m.lib.fnLlamaModelNEmbd(m.ptr))
}

// NCtxTrain returns the context size the model was trained with.
func (m *Model) NCtxTrain() int {
	return int(m.lib.fnLlamaModelNCtxTrain(m.ptr))
}

// NLayer returns the number of layers in the model.
func (m *Model) NLayer() int {
	return int(m.lib.fnLlamaModelNLayer(m.ptr))
}

// Size returns the total size of the model in bytes.
func (m *Model) Size() uint64 {
	return m.lib.fnLlamaModelSize(m.ptr)
}

// NParams returns the number of parameters in the model.
func (m *Model) NParams() uint64 {
	return m.lib.fnLlamaModelNParams(m.ptr)
}

// Desc returns a human-readable model description (e.g. "7B Q4_0").
func (m *Model) Desc() string {
	bufSize := int32(256)
	buf := make([]byte, bufSize)
	n := m.lib.fnLlamaModelDesc(m.ptr, &buf[0], uintptr(bufSize))
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}

// MetaValStr reads a model metadata value by key name.
// Returns empty string if key not found.
func (m *Model) MetaValStr(key string) string {
	keyPtr, keyBuf := cstr(key)
	bufSize := int32(256)
	buf := make([]byte, bufSize)
	n := m.lib.fnLlamaModelMetaValStr(m.ptr, keyPtr, &buf[0], uintptr(bufSize))
	runtime.KeepAlive(keyBuf)
	if n < 0 {
		return ""
	}
	if n > bufSize {
		buf = make([]byte, n)
		m.lib.fnLlamaModelMetaValStr(m.ptr, keyPtr, &buf[0], uintptr(n))
		runtime.KeepAlive(keyBuf)
		return strings.TrimRight(string(buf[:n]), "\x00")
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}

// MetaCount returns the number of metadata key/value pairs.
func (m *Model) MetaCount() int {
	return int(m.lib.fnLlamaModelMetaCount(m.ptr))
}

// ChatTemplate returns the chat template string embedded in the model.
// Pass empty name for the default template. Returns empty string if not found.
func (m *Model) ChatTemplate(name string) string {
	var namePtr *byte
	var nameBuf []byte
	if name != "" {
		namePtr, nameBuf = cstr(name)
	}
	p := m.lib.fnLlamaModelChatTpl(m.ptr, namePtr)
	runtime.KeepAlive(nameBuf)
	return gostr(p)
}

// Vocab returns the internal vocab pointer.
func (m *Model) Vocab() uintptr {
	if m.vocab == 0 {
		m.vocab = m.lib.fnLlamaModelGetVocab(m.ptr)
	}
	return m.vocab
}

// ApplyLoraFromFile loads and applies a LoRA adapter to the context.
func (m *Model) ApplyLoraFromFile(context *Context, loraPath string, scale float32, threads int) error {
	pathPtr, pathBuf := cstr(loraPath)
	adapter := m.lib.fnLlamaAdapterLoraInit(m.ptr, pathPtr)
	runtime.KeepAlive(pathBuf)
	if adapter == 0 {
		return fmt.Errorf("unable to load lora: %s", loraPath)
	}

	// b8721+: llama_set_adapters_lora takes arrays of adapter pointers and scales.
	adapters := [1]uintptr{adapter}
	scales := [1]float32{scale}
	code := m.lib.fnLlamaSetAdaptersLora(
		context.ptr,
		uintptr(unsafe.Pointer(&adapters[0])),
		1,
		uintptr(unsafe.Pointer(&scales[0])),
	)
	if code != 0 {
		return fmt.Errorf("error applying lora from file: %s", loraPath)
	}
	return nil
}

// ── Package-level model functions ───────────────────────────────────────────

// LoadModelFromFile loads a model from the given GGUF file.
func LoadModelFromFile(lib *Library, modelPath string, params ModelParams) (*Model, error) {
	// Get default params via typed function
	cp := lib.fnLlamaModelDefaultParams()

	cp.NGPULayers = int32(params.NumGpuLayers)
	cp.MainGPU = int32(params.MainGpu) // -1 = library auto-select
	cp.UseMmap = params.UseMmap
	cp.VocabOnly = params.VocabOnly

	// Set devices
	var devsBuf []uintptr
	if len(params.Devices) > 0 {
		devsBuf = make([]uintptr, len(params.Devices)+1) // NULL-terminated
		for i, id := range params.Devices {
			devsBuf[i] = lib.fnGgmlBackendDevGet(uintptr(id))
		}
		devsBuf[len(params.Devices)] = 0
		cp.Devices = uintptr(unsafe.Pointer(&devsBuf[0]))
	}

	// Set tensor split
	if len(params.TensorSplit) > 0 {
		cp.TensorSplit = uintptr(unsafe.Pointer(&params.TensorSplit[0]))
	}

	// Set progress callback
	var progressCb uintptr
	if params.Progress != nil {
		progressCb = newProgressCallback(params.Progress)
		cp.ProgressCallback = progressCb
	}

	pathPtr, pathBuf := cstr(modelPath)
	mPtr := lib.fnLlamaModelLoadFromFile(pathPtr, cp)
	runtime.KeepAlive(pathBuf)
	runtime.KeepAlive(devsBuf)
	runtime.KeepAlive(params.TensorSplit)

	if mPtr == 0 {
		return nil, fmt.Errorf("%w: %s", ErrModelLoad, modelPath)
	}

	return &Model{ptr: mPtr, lib: lib}, nil
}

// FreeModel releases a loaded model.
func FreeModel(model *Model) {
	if model != nil && model.ptr != 0 {
		model.lib.fnLlamaModelFree(model.ptr)
		model.ptr = 0
	}
}

// SystemInfo returns a string with system information (CPU features, build flags, etc.).
func SystemInfo(lib *Library) string {
	p := lib.fnLlamaPrintSystemInfo()
	return gostr(p)
}
