package browser

import "context"

type Service interface {
	Install(context.Context, Request) (map[string]any, error)
	Status(context.Context, Request) (map[string]any, error)
	Start(context.Context, Request) (map[string]any, error)
	Stop(context.Context, Request) (map[string]any, error)
	Profiles(context.Context, Request) (map[string]any, error)
	Tabs(context.Context, Request) (map[string]any, error)
	Open(context.Context, OpenRequest) (map[string]any, error)
	Focus(context.Context, TargetRequest) (map[string]any, error)
	Close(context.Context, TargetRequest) (map[string]any, error)
	Navigate(context.Context, NavigateRequest) (map[string]any, error)
	Snapshot(context.Context, SnapshotRequest) (map[string]any, error)
	Act(context.Context, ActRequest) (map[string]any, error)
	Upload(context.Context, UploadRequest) (map[string]any, error)
	Dialog(context.Context, DialogRequest) (map[string]any, error)
	Screenshot(context.Context, ScreenshotRequest) (map[string]any, error)
	PDF(context.Context, TargetRequest) (map[string]any, error)
	Console(context.Context, ConsoleRequest) (map[string]any, error)
	Errors(context.Context, DebugRequest) (map[string]any, error)
	Requests(context.Context, RequestsRequest) (map[string]any, error)
	TraceStart(context.Context, TraceStartRequest) (map[string]any, error)
	TraceStop(context.Context, TraceStopRequest) (map[string]any, error)
	Download(context.Context, DownloadRequest) (map[string]any, error)
	WaitDownload(context.Context, WaitDownloadRequest) (map[string]any, error)
}

type Options struct {
	ArtifactDir    string
	DefaultProfile string
	Headless       *bool
	DriverDir      string
	AutoInstall    bool

	// UseSystemBrowser enables detection and use of a system-installed browser
	// (Chrome, Edge) instead of the Playwright-bundled Chromium.
	// When true, SkipInstallBrowsers is implicitly set.
	UseSystemBrowser bool

	// Channel specifies the browser distribution channel to use.
	// Supported: "chrome", "msedge", or empty for auto-detect / bundled.
	Channel string

	// SkipInstallBrowsers skips downloading browsers during Install.
	// Useful when using a system browser or pre-bundled driver-only setup.
	SkipInstallBrowsers bool

	// PersistSession enables persistent browser context so that cookies,
	// localStorage, and other session data survive browser restarts.
	PersistSession bool

	// UserDataDir is the directory used for persistent session storage.
	// When empty and PersistSession is true, defaults to <ArtifactDir>/userdata.
	UserDataDir string
}

type Request struct {
	SessionKey string
	Target     string
	Profile    string
	Node       string
	TimeoutMs  int
	Headless   *bool // per-request headless override; nil = use manager default
}

type OpenRequest struct {
	Request
	URL       string
	TargetURL string
}

type TargetRequest struct {
	Request
	TargetID string
}

type NavigateRequest struct {
	TargetRequest
	URL       string
	TargetURL string
}

type ScreenshotRequest struct {
	TargetRequest
	Type     string
	FullPage bool
}

type SnapshotRequest struct {
	TargetRequest
	Format      string
	Refs        string
	Selector    string
	Frame       string
	Mode        string
	Limit       int
	MaxChars    int
	Depth       int
	Interactive bool
	Compact     bool
	Labels      bool
}

type ActRequest struct {
	TargetRequest
	Kind        string
	Ref         string
	StartRef    string
	EndRef      string
	Element     string
	Selector    string
	Text        string
	Key         string
	Fn          string
	TimeoutMs   int
	TimeMs      int
	LoadState   string
	URL         string
	TextGone    string
	Slowly      bool
	Submit      bool
	Values      []string
	Width       int
	Height      int
	DoubleClick bool
	Button      string
	Modifiers   []string
	DelayMs     int
}

type UploadRequest struct {
	TargetRequest
	Ref      string
	InputRef string
	Element  string
	Selector string
	Paths    []string
}

type DialogRequest struct {
	TargetRequest
	Accept     bool
	PromptText string
}

type ConsoleRequest struct {
	TargetRequest
	Level string
	Limit int
}

type DebugRequest struct {
	TargetRequest
	Clear bool
	Limit int
}

type RequestsRequest struct {
	DebugRequest
	Filter string
}

type TraceStartRequest struct {
	TargetRequest
	Screenshots bool
	Snapshots   bool
	Sources     bool
}

type TraceStopRequest struct {
	TargetRequest
	Path string
}

type DownloadRequest struct {
	TargetRequest
	Ref  string
	Path string
}

type WaitDownloadRequest struct {
	TargetRequest
	Path string
}
