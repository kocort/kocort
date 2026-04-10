package manager

import (
	"context"
	"net/http"

	"github.com/kocort/kocort/internal/infra"
)

// ── Actor commands ──────────────────────────────────────────────────────────

// cmd is a command sent to the Manager's actor goroutine.
type cmd interface{}

type cmdStart struct{ reply chan<- error }
type cmdStop struct{ reply chan<- error }
type cmdRestart struct{ reply chan<- error }

type cmdSelectModel struct {
	modelID string
	reply   chan<- error
}

type cmdClearModel struct{ reply chan<- error }

type cmdDeleteModel struct {
	modelID string
	reply   chan<- error
}

type cmdUpdateAllParams struct {
	sp                              *SamplingParams
	threads, contextSize, gpuLayers int
	reply                           chan<- error
}

type cmdSetSamplingParams struct {
	sp    SamplingParams
	reply chan<- error
}

type cmdUpdateRuntimeParams struct {
	threads, contextSize, gpuLayers int
	reply                           chan<- error
}

type cmdSetEnableThinking struct{ enabled bool }
type cmdSetDynamicHTTPClient struct{ dc *infra.DynamicHTTPClient }
type cmdSetCatalog struct{ catalog []ModelPreset }

type cmdDownloadModel struct {
	presetID   string
	httpClient *http.Client
	reply      chan<- error
}

type cmdCancelDownload struct{ reply chan<- error }
type cmdSnapshot struct{ reply chan<- Snapshot }
type cmdWaitReady struct{ reply chan<- string }
type cmdGetModels struct{ reply chan<- []ModelInfo }
type cmdClose struct{}

// cmdInfer is sent when a caller wants to run streaming inference.
// The actor validates the model state and dispatches to the backend.
type cmdInfer struct {
	ctx   context.Context
	req   ChatCompletionRequest
	reply chan<- inferResult
}

type inferResult struct {
	ch  <-chan ChatCompletionChunk
	err error
}

// Internal completion events sent by background goroutines back to the actor.
type cmdLifecycleDone struct {
	err         error
	contextSize int    // >0 after successful start/restart
	op          string // "start", "stop", "restart", "stop-for-pending"
}

type cmdStatusHint struct{ status string }

type cmdDLDone struct {
	err      error
	canceled bool
}

// pendingOp describes a compound operation that must complete after an
// async stop (e.g. delete-after-stop, clear-after-stop).
type pendingOp struct {
	kind    string // "delete" or "clear"
	modelID string
	reply   chan<- error
}
