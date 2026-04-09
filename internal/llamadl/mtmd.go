package llamadl

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"unsafe"
)

// MtmdContext wraps a mtmd_context pointer for multimodal processing.
type MtmdContext struct {
	ptr uintptr // *mtmd_context
	lib *Library
}

// MtmdChunk represents a single chunk from multimodal tokenization —
// either a text chunk (with tokens) or an image chunk (with embeddings).
type MtmdChunk struct {
	Embed  []float32
	Tokens []int
}

// NewMtmdContext creates a new multimodal context for the given model.
func NewMtmdContext(lib *Library, llamaContext *Context, modelPath string) (*MtmdContext, error) {
	if !lib.IsMtmdAvailable() {
		return nil, errors.New("mtmd library not available")
	}

	pathPtr, pathBuf := cstr(modelPath)

	// Get default params via typed function
	cp := lib.fnMtmdContextParamsDefault()

	modelPtr := lib.fnLlamaGetModel(llamaContext.ptr)
	ctx := lib.fnMtmdInitFromFile(pathPtr, modelPtr, uintptr(unsafe.Pointer(&cp)))
	runtime.KeepAlive(pathBuf)

	if ctx == 0 {
		return nil, fmt.Errorf("unable to load mtmd model: %s", modelPath)
	}

	return &MtmdContext{ptr: ctx, lib: lib}, nil
}

// Free releases the multimodal context.
func (c *MtmdContext) Free() {
	if c.ptr != 0 {
		c.lib.fnMtmdFree(c.ptr)
		c.ptr = 0
	}
}

// MultimodalTokenize processes image data into tokenized chunks
// (text token chunks and image embedding chunks).
func (c *MtmdContext) MultimodalTokenize(llamaContext *Context, data []byte) ([]MtmdChunk, error) {
	lib := c.lib

	// Initialize input chunks
	ic := lib.fnMtmdInputChunksInit()
	defer lib.fnMtmdInputChunksFree(ic)

	// Initialize empty text prompt
	marker := lib.fnMtmdDefaultMarker()
	if lib.fnMtmdInputTextInit == nil {
		return nil, errors.New("mtmd_input_text_init not available in this llama.cpp version")
	}
	it := lib.fnMtmdInputTextInit(marker, true, true)
	defer lib.fnMtmdInputTextFree(it)

	// Initialize bitmap from image data
	bm := lib.fnMtmdHelperBitmapInitFromBuf(c.ptr, &data[0], uintptr(len(data)))
	if bm == 0 {
		return nil, errors.New("unable to create bitmap from image data")
	}
	defer lib.fnMtmdBitmapFree(bm)

	// Tokenize
	ret := lib.fnMtmdTokenize(c.ptr, ic, it, (*uintptr)(unsafe.Pointer(&bm)), 1)
	if ret != 0 {
		return nil, errors.New("unable to tokenize mtmd embedding from image")
	}

	nChunks := lib.fnMtmdInputChunksSize(ic)
	numEmbed := llamaContext.Model().NEmbd()
	outChunks := make([]MtmdChunk, 0)

	for i := uintptr(0); i < nChunks; i++ {
		chunk := lib.fnMtmdInputChunksGet(ic, i)
		numTokens := int(lib.fnMtmdInputChunkGetNTokens(chunk))
		slog.Debug("chunk tokens", "index", i, "numTokens", numTokens)

		chunkType := lib.fnMtmdInputChunkGetType(chunk)

		if chunkType == MtmdInputChunkTypeText {
			// Text chunk — extract tokens
			var cNumTokens uintptr
			cTokensPtr := lib.fnMtmdInputChunkGetTokensText(chunk, &cNumTokens)
			cTokensArr := unsafe.Slice((*int32)(unsafe.Pointer(cTokensPtr)), int(cNumTokens))
			tokens := make([]int, int(cNumTokens))
			for j := range int(cNumTokens) {
				tokens[j] = int(cTokensArr[j])
			}
			outChunks = append(outChunks, MtmdChunk{Tokens: tokens})
		} else {
			// Image/audio chunk — encode to embeddings
			ret := lib.fnMtmdEncodeChunk(c.ptr, chunk)
			if ret != 0 {
				return nil, errors.New("unable to encode mtmd image chunk")
			}

			chunkEmbd := lib.fnMtmdGetOutputEmbd(c.ptr)
			if chunkEmbd == 0 {
				return nil, errors.New("no mtmd image embedding")
			}

			// Copy embeddings for each token
			s := unsafe.Slice((*float32)(unsafe.Pointer(chunkEmbd)), numTokens*numEmbed)
			rows := make([]float32, len(s))
			copy(rows, s)
			for j := range numTokens {
				outChunks = append(outChunks, MtmdChunk{
					Embed: rows[j*numEmbed : (j+1)*numEmbed],
				})
			}
		}
	}

	slog.Debug("image tokenization chunks", "totalChunks", len(outChunks))
	return outChunks, nil
}
