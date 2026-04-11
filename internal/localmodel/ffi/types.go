package ffi

import "unsafe"

// ── C ABI compatible types ──────────────────────────────────────────────────
// All struct layouts must match the C definitions in llama.h / ggml.h exactly.
// Verified against llama.cpp b8720+ on darwin/arm64 and linux/amd64.

// llama_token is int32_t in C.
type llamaToken = int32

// llama_pos is int32_t in C.
type llamaPos = int32

// llama_seq_id is int32_t in C.
type llamaSeqID = int32

// ── Enums ───────────────────────────────────────────────────────────────────

// ggml_type enum values (commonly used for KV cache type).
const (
	GGMLTypeF32  int32 = 0
	GGMLTypeF16  int32 = 1
	GGMLTypeQ4_0 int32 = 2
	GGMLTypeQ4_1 int32 = 3
	GGMLTypeQ5_0 int32 = 6
	GGMLTypeQ5_1 int32 = 7
	GGMLTypeQ8_0 int32 = 8
	GGMLTypeQ8_1 int32 = 9
)

// llama_flash_attn_type enum values.
const (
	LlamaFlashAttnTypeAuto     int32 = -1
	LlamaFlashAttnTypeDisabled int32 = 0
	LlamaFlashAttnTypeEnabled  int32 = 1
)

// ggml_backend_device_type enum values.
const (
	GGMLBackendDeviceTypeCPU  int32 = 0
	GGMLBackendDeviceTypeGPU  int32 = 1
	GGMLBackendDeviceTypeIGPU int32 = 2
)

// mtmd_input_chunk_type enum values.
const (
	MtmdInputChunkTypeText  int32 = 0
	MtmdInputChunkTypeImage int32 = 1
	MtmdInputChunkTypeAudio int32 = 2
)

// ── llama_model_params ──────────────────────────────────────────────────────
// Must match the C struct layout. Fields are ordered to match the C definition.
type cModelParams struct {
	Devices                  uintptr // ggml_backend_dev_t * (NULL-terminated array)
	TensorBuftOverrides      uintptr // const struct llama_model_tensor_buft_override *
	NGPULayers               int32
	SplitMode                int32 // enum llama_split_mode
	MainGPU                  int32
	_pad0                    [4]byte // alignment padding
	TensorSplit              uintptr // const float *
	ProgressCallback         uintptr // llama_progress_callback
	ProgressCallbackUserData uintptr // void *
	KVOverrides              uintptr // const struct llama_model_kv_override *
	VocabOnly                cbool
	UseMmap                  cbool
	UseDirectIO              cbool
	UseMlock                 cbool
	CheckTensors             cbool
	UseExtraBuffs            cbool
	NoHost                   cbool
	NoAlloc                  cbool
}

// ── llama_context_params ────────────────────────────────────────────────────
type cContextParams struct {
	NCtx              uint32
	NBatch            uint32
	NUbatch           uint32
	NSeqMax           uint32
	NThreads          int32
	NThreadsBatch     int32
	RopeScalingType   int32
	PoolingType       int32
	AttentionType     int32
	FlashAttnType     int32
	RopeFreqBase      float32
	RopeFreqScale     float32
	YarnExtFactor     float32
	YarnAttnFactor    float32
	YarnBetaFast      float32
	YarnBetaSlow      float32
	YarnOrigCtx       uint32
	DefragThold       float32
	CbEval            uintptr // ggml_backend_sched_eval_callback
	CbEvalUserData    uintptr // void *
	TypeK             int32   // enum ggml_type
	TypeV             int32   // enum ggml_type
	AbortCallback     uintptr // ggml_abort_callback
	AbortCallbackData uintptr // void *
	Embeddings        cbool
	OffloadKQV        cbool
	NoPerf            cbool
	OpOffload         cbool
	SWAFull           cbool
	KVUnified         cbool
	_pad1             [2]byte // alignment padding to pointer
	Samplers          uintptr // const struct llama_sampler ** (currently unused)
	NSamplers         uintptr // size_t
}

// ── llama_batch ─────────────────────────────────────────────────────────────
type cBatch struct {
	NTokens int32
	_pad    [4]byte // padding to align Token pointer
	Token   uintptr // llama_token *
	Embd    uintptr // float *
	Pos     uintptr // llama_pos *
	NSeqID  uintptr // int32_t *
	SeqID   uintptr // llama_seq_id **
	Logits  uintptr // int8_t *
}

// ── llama_token_data ────────────────────────────────────────────────────────
type cTokenData struct {
	ID    int32
	Logit float32
	P     float32
}

// ── llama_token_data_array ──────────────────────────────────────────────────
type cTokenDataArray struct {
	Data     uintptr // llama_token_data *
	Size     uintptr // size_t
	Selected int64
	Sorted   cbool
	_pad     [7]byte
}

// ── llama_sampler_chain_params ──────────────────────────────────────────────
type cSamplerChainParams struct {
	NoPerf cbool
	_pad   [7]byte
}

// ── gguf_init_params ────────────────────────────────────────────────────────
type cGgufInitParams struct {
	NoAlloc cbool
	_pad    [7]byte
	Ctx     uintptr // struct ggml_context **
}

// ── ggml_backend_dev_props ──────────────────────────────────────────────────
// Mirrors the C struct ggml_backend_dev_props (b8720):
//
//	struct ggml_backend_dev_props {
//	    const char * name;
//	    const char * description;
//	    size_t       memory_free;
//	    size_t       memory_total;
//	    enum ggml_backend_dev_type type;  // int32
//	    const char * device_id;           // may be NULL
//	    struct ggml_backend_dev_caps caps; // 4 bools
//	};
type cBackendDevProps struct {
	Name     uintptr // const char *
	Desc     uintptr // const char *
	MemFree  uint64  // size_t
	MemTotal uint64  // size_t
	Type     int32   // enum
	_pad0    [4]byte // align next pointer
	DeviceID uintptr // const char * (may be 0/NULL)
	Caps     [4]bool // ggml_backend_dev_caps {async, host_buffer, buffer_from_host_ptr, events}
	_pad1    [4]byte // pad to 8-byte boundary
}

// ── mtmd_context_params ─────────────────────────────────────────────────────
type cMtmdContextParams struct {
	UseGPU    cbool
	_pad0     [3]byte
	NThreads  int32
	Verbosity int32
	_pad1     [4]byte
}

// ── cbool type ──────────────────────────────────────────────────────────────
// C bool is 1 byte. Go bool may have different ABI guarantees, so we use
// a dedicated type to ensure correct layout.
type cbool = bool

// ── Helper functions ────────────────────────────────────────────────────────

// cstr converts a Go string to a null-terminated C string.
// The returned pointer is backed by a Go []byte — caller must keep a reference
// to prevent GC collection during the C call.
func cstr(s string) (*byte, []byte) {
	b := append([]byte(s), 0)
	return &b[0], b
}

// gostr converts a C null-terminated string to a Go string.
func gostr(p *byte) string {
	if p == nil {
		return ""
	}
	var buf []byte
	for ptr := unsafe.Pointer(p); *(*byte)(ptr) != 0; ptr = unsafe.Add(ptr, 1) {
		buf = append(buf, *(*byte)(ptr))
	}
	return string(buf)
}

// gostrN reads a null-terminated string from a byte array.
func gostrN(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
