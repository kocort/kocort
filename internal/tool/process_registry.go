package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

const (
	defaultProcessMaxOutputChars     = 200_000
	defaultProcessPendingOutputChars = 30_000
	defaultProcessTailChars          = 4_000
	defaultProcessCleanupTTL         = 30 * time.Minute
	defaultProcessMaxPollWait        = 2 * time.Minute
)

type ProcessMode string

const (
	ProcessModeChild ProcessMode = "child"
	ProcessModePTY   ProcessMode = "pty"
)

type ProcessTerminationReason string

const (
	ProcessTerminationManualCancel    ProcessTerminationReason = "manual_cancel"
	ProcessTerminationOverallTimeout  ProcessTerminationReason = "overall_timeout"
	ProcessTerminationNoOutputTimeout ProcessTerminationReason = "no_output_timeout"
	ProcessTerminationSpawnError      ProcessTerminationReason = "spawn_error"
	ProcessTerminationSignal          ProcessTerminationReason = "signal"
	ProcessTerminationExit            ProcessTerminationReason = "exit"
)

// ProcessSessionRecord describes a managed process session.
type ProcessSessionRecord struct {
	ID                string                   `json:"id"`
	SessionKey        string                   `json:"sessionKey,omitempty"`
	RunID             string                   `json:"runId,omitempty"`
	ScopeKey          string                   `json:"scopeKey,omitempty"`
	Command           string                   `json:"command"`
	Workdir           string                   `json:"workdir,omitempty"`
	StartedAt         time.Time                `json:"startedAt"`
	EndedAt           *time.Time               `json:"endedAt,omitempty"`
	LastOutputAt      time.Time                `json:"lastOutputAt"`
	Status            string                   `json:"status"`
	State             string                   `json:"state,omitempty"`
	Mode              ProcessMode              `json:"mode,omitempty"`
	Backgrounded      bool                     `json:"backgrounded,omitempty"`
	PID               int                      `json:"pid,omitempty"`
	ExitCode          *int                     `json:"exitCode,omitempty"`
	ExitSignal        string                   `json:"exitSignal,omitempty"`
	TerminationReason ProcessTerminationReason `json:"terminationReason,omitempty"`
	TimedOut          bool                     `json:"timedOut,omitempty"`
	NoOutputTimedOut  bool                     `json:"noOutputTimedOut,omitempty"`
	Error             string                   `json:"error,omitempty"`
	Tail              string                   `json:"tail,omitempty"`
	Output            string                   `json:"output,omitempty"`
	Truncated         bool                     `json:"truncated,omitempty"`
	TotalOutputChars  int                      `json:"totalOutputChars,omitempty"`
}

type ProcessRegistryOptions struct {
	CleanupTTL         time.Duration
	MaxOutputChars     int
	PendingOutputChars int
}

type processSessionState struct {
	record           ProcessSessionRecord
	cancel           context.CancelFunc
	cmd              *exec.Cmd
	ptyFile          *osFileCloser
	stdin            io.WriteCloser
	done             chan struct{}
	waitErr          error
	killed           bool
	exited           bool
	outputSeq        int64
	pendingOutput    string
	maxOutput        int
	pendingOutputCap int
	cond             *sync.Cond
	noOutputTimer    *time.Timer
	noOutputWait     time.Duration
	cleanupDeadline  time.Time
}

// osFileCloser is the small subset we need from *os.File for PTY sessions.
type osFileCloser struct {
	io.ReadWriteCloser
}

// ProcessRegistry manages running and recent background processes.
type ProcessRegistry struct {
	mu        sync.Mutex
	running   map[string]*processSessionState
	finished  map[string]*processSessionState
	opts      ProcessRegistryOptions
	sweeper   *time.Ticker
	stopSweep chan struct{}
}

// ProcessStartOptions configures a new process start.
type ProcessStartOptions struct {
	SessionKey         string
	RunID              string
	ScopeKey           string
	Command            string
	Workdir            string
	Env                []string
	Timeout            time.Duration
	NoOutputTimeout    time.Duration
	Backgrounded       bool
	MaxOutputChars     int
	PendingOutputChars int
	PTY                bool
}

func NewProcessRegistry(opts ...ProcessRegistryOptions) *ProcessRegistry {
	cfg := ProcessRegistryOptions{
		CleanupTTL:         defaultProcessCleanupTTL,
		MaxOutputChars:     defaultProcessMaxOutputChars,
		PendingOutputChars: defaultProcessPendingOutputChars,
	}
	if len(opts) > 0 {
		if opts[0].CleanupTTL > 0 {
			cfg.CleanupTTL = opts[0].CleanupTTL
		}
		if opts[0].MaxOutputChars > 0 {
			cfg.MaxOutputChars = opts[0].MaxOutputChars
		}
		if opts[0].PendingOutputChars > 0 {
			cfg.PendingOutputChars = opts[0].PendingOutputChars
		}
	}
	r := &ProcessRegistry{
		running:   map[string]*processSessionState{},
		finished:  map[string]*processSessionState{},
		opts:      cfg,
		stopSweep: make(chan struct{}),
	}
	r.startSweeper()
	return r
}

func (r *ProcessRegistry) Start(_ context.Context, opts ProcessStartOptions) (ProcessSessionRecord, error) {
	if r == nil {
		return ProcessSessionRecord{}, core.ErrProcessNotConfigured
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		return ProcessSessionRecord{}, fmt.Errorf("command must not be empty")
	}
	procCtx := context.Background()
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		procCtx, cancel = context.WithTimeout(procCtx, opts.Timeout)
	} else {
		procCtx, cancel = context.WithCancel(procCtx)
	}
	now := time.Now().UTC()
	state := &processSessionState{
		record: ProcessSessionRecord{
			ID:           newID("proc_"),
			SessionKey:   strings.TrimSpace(opts.SessionKey),
			RunID:        strings.TrimSpace(opts.RunID),
			ScopeKey:     strings.TrimSpace(opts.ScopeKey),
			Command:      command,
			Workdir:      strings.TrimSpace(opts.Workdir),
			StartedAt:    now,
			LastOutputAt: now,
			Status:       "running",
			State:        "starting",
			Mode:         ProcessModeChild,
			Backgrounded: opts.Backgrounded,
		},
		cancel:           cancel,
		done:             make(chan struct{}),
		maxOutput:        max(defaultProcessMaxOutputChars, resolveProcessMaxOutput(opts.MaxOutputChars, r.opts.MaxOutputChars)),
		pendingOutputCap: max(defaultProcessPendingOutputChars, resolveProcessPendingOutput(opts.PendingOutputChars, r.opts.PendingOutputChars)),
	}
	if opts.PTY {
		state.record.Mode = ProcessModePTY
	}
	state.cond = sync.NewCond(&r.mu)

	cmd, stdout, stderr, stdin, ptyFile, err := startManagedCommand(procCtx, command, opts)
	if err != nil {
		cancel()
		state.record.Status = "failed"
		state.record.State = "exited"
		state.record.Error = err.Error()
		state.record.TerminationReason = ProcessTerminationSpawnError
		now := time.Now().UTC()
		state.record.EndedAt = &now
		return state.record, err
	}
	state.cmd = cmd
	state.stdin = stdin
	state.ptyFile = ptyFile
	state.record.PID = processID(cmd)
	state.record.State = "running"

	r.mu.Lock()
	r.running[state.record.ID] = state
	r.mu.Unlock()

	state.noOutputWait = opts.NoOutputTimeout
	if opts.NoOutputTimeout > 0 {
		r.armNoOutputTimer(state)
	}

	if stdout != nil {
		go r.captureOutput(state, stdout)
	}
	if stderr != nil {
		go r.captureOutput(state, stderr)
	}
	go r.wait(state, procCtx)

	return cloneProcessRecord(state.record), nil
}

func startManagedCommand(procCtx context.Context, command string, opts ProcessStartOptions) (*exec.Cmd, io.ReadCloser, io.ReadCloser, io.WriteCloser, *osFileCloser, error) {
	if opts.PTY {
		if !platformSupportsPTY() {
			return nil, nil, nil, nil, nil, fmt.Errorf("pty mode is not supported on %s yet", runtime.GOOS)
		}
		cmd, err := infra.CommandShellContext(procCtx, command)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if strings.TrimSpace(opts.Workdir) != "" {
			cmd.Dir = strings.TrimSpace(opts.Workdir)
		}
		if len(opts.Env) > 0 {
			cmd.Env = append([]string{}, opts.Env...)
		}
		ptmx, err := pty.Start(cmd)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		file := &osFileCloser{ReadWriteCloser: ptmx}
		return cmd, file, nil, ptmx, file, nil
	}
	cmd, err := infra.CommandShellContext(procCtx, command)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if strings.TrimSpace(opts.Workdir) != "" {
		cmd.Dir = strings.TrimSpace(opts.Workdir)
	}
	if len(opts.Env) > 0 {
		cmd.Env = append([]string{}, opts.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return cmd, stdout, stderr, stdin, nil, nil
}

func (r *ProcessRegistry) List() []ProcessSessionRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProcessSessionRecord, 0, len(r.running)+len(r.finished))
	for _, state := range r.running {
		if state.record.Backgrounded {
			out = append(out, cloneProcessRecord(state.record))
		}
	}
	for _, state := range r.finished {
		if state.record.Backgrounded {
			out = append(out, cloneProcessRecord(state.record))
		}
	}
	return out
}

func (r *ProcessRegistry) Get(sessionID string) (ProcessSessionRecord, bool) {
	state, ok := r.getState(strings.TrimSpace(sessionID))
	if !ok {
		return ProcessSessionRecord{}, false
	}
	return cloneProcessRecord(state.record), true
}

func (r *ProcessRegistry) Poll(sessionID string, wait time.Duration) (ProcessSessionRecord, bool) {
	if r == nil {
		return ProcessSessionRecord{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ProcessSessionRecord{}, false
	}
	if wait < 0 {
		wait = 0
	}
	if wait > defaultProcessMaxPollWait {
		wait = defaultProcessMaxPollWait
	}
	deadline := time.Now().Add(wait)

	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.getStateLocked(sessionID)
	if !ok {
		return ProcessSessionRecord{}, false
	}
	for wait > 0 && state.record.Status == "running" {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		timer := time.AfterFunc(remaining, func() {
			r.mu.Lock()
			state.cond.Broadcast()
			r.mu.Unlock()
		})
		state.cond.Wait()
		timer.Stop()
		state, ok = r.getStateLocked(sessionID)
		if !ok {
			return ProcessSessionRecord{}, false
		}
	}
	return cloneProcessRecord(state.record), true
}

func (r *ProcessRegistry) Write(sessionID string, data string, eof bool) (ProcessSessionRecord, bool, error) {
	r.mu.Lock()
	state, ok := r.getStateLocked(strings.TrimSpace(sessionID))
	if !ok {
		r.mu.Unlock()
		return ProcessSessionRecord{}, false, nil
	}
	if state.record.Status != "running" {
		record := cloneProcessRecord(state.record)
		r.mu.Unlock()
		return record, true, fmt.Errorf("process session %q is not running", sessionID)
	}
	stdin := state.stdin
	if stdin == nil {
		record := cloneProcessRecord(state.record)
		r.mu.Unlock()
		return record, true, fmt.Errorf("process session %q stdin is not writable", sessionID)
	}
	r.mu.Unlock()
	if data != "" {
		if _, err := io.WriteString(stdin, data); err != nil {
			return ProcessSessionRecord{}, true, err
		}
	}
	if eof {
		if err := stdin.Close(); err != nil {
			return ProcessSessionRecord{}, true, err
		}
	}
	record, _ := r.Get(sessionID)
	return record, true, nil
}

func (r *ProcessRegistry) Submit(sessionID string) (ProcessSessionRecord, bool, error) {
	return r.Write(sessionID, "\n", false)
}

func (r *ProcessRegistry) Paste(sessionID string, text string) (ProcessSessionRecord, bool, error) {
	return r.Write(sessionID, text, false)
}

func (r *ProcessRegistry) Kill(sessionID string) (ProcessSessionRecord, bool, error) {
	if r == nil {
		return ProcessSessionRecord{}, false, core.ErrProcessNotConfigured
	}
	sessionID = strings.TrimSpace(sessionID)
	r.mu.Lock()
	state, ok := r.getStateLocked(sessionID)
	if !ok {
		r.mu.Unlock()
		return ProcessSessionRecord{}, false, nil
	}
	if state.record.Status != "running" {
		record := cloneProcessRecord(state.record)
		r.mu.Unlock()
		return record, true, nil
	}
	state.killed = true
	state.record.State = "exiting"
	state.record.TerminationReason = ProcessTerminationManualCancel
	cancel := state.cancel
	cmd := state.cmd
	ptyFile := state.ptyFile
	r.mu.Unlock()

	cancel()
	if ptyFile != nil {
		_ = ptyFile.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	record, _ := r.Poll(sessionID, 250*time.Millisecond)
	return record, true, nil
}

func (r *ProcessRegistry) Clear(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.finished[strings.TrimSpace(sessionID)]
	if !ok {
		return false
	}
	r.disposeStateLocked(state)
	delete(r.finished, strings.TrimSpace(sessionID))
	return true
}

func (r *ProcessRegistry) Remove(sessionID string) (ProcessSessionRecord, bool, error) {
	if record, ok := r.Get(sessionID); ok && record.Status == "running" {
		return r.Kill(sessionID)
	}
	record, ok := r.Get(sessionID)
	if !ok {
		return ProcessSessionRecord{}, false, nil
	}
	if !r.Clear(sessionID) {
		return ProcessSessionRecord{}, false, nil
	}
	return record, true, nil
}

func (r *ProcessRegistry) captureOutput(state *processSessionState, reader io.ReadCloser) {
	defer reader.Close()
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			r.appendOutput(state, string(buffer[:n]))
		}
		if err != nil {
			if err != io.EOF && !errors.Is(err, fs.ErrClosed) && !strings.Contains(strings.ToLower(err.Error()), "file already closed") {
				r.appendOutput(state, "\n"+err.Error())
			}
			return
		}
	}
}

func (r *ProcessRegistry) appendOutput(state *processSessionState, chunk string) {
	if chunk == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state.record.LastOutputAt = time.Now().UTC()
	state.pendingOutput = trimWithCap(state.pendingOutput+chunk, state.pendingOutputCap)
	state.record.Output = trimWithCap(state.record.Output+chunk, state.maxOutput)
	state.record.Tail = tailString(state.record.Output, defaultProcessTailChars)
	state.record.TotalOutputChars += len(chunk)
	if len(state.record.Output) < state.record.TotalOutputChars {
		state.record.Truncated = true
	}
	r.armNoOutputTimer(state)
	state.outputSeq++
	state.cond.Broadcast()
}

func (r *ProcessRegistry) wait(state *processSessionState, procCtx context.Context) {
	err := state.cmd.Wait()
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	state.waitErr = err
	state.exited = true
	state.record.EndedAt = &now
	state.record.Tail = tailString(state.record.Output, defaultProcessTailChars)
	state.record.State = "exited"
	if state.noOutputTimer != nil {
		state.noOutputTimer.Stop()
		state.noOutputTimer = nil
	}
	switch {
	case state.killed || errors.Is(procCtx.Err(), context.Canceled):
		state.record.Status = "killed"
		state.record.Error = "killed"
		if state.record.TerminationReason == "" {
			state.record.TerminationReason = ProcessTerminationManualCancel
		}
	case errors.Is(procCtx.Err(), context.DeadlineExceeded):
		state.record.Status = "timed_out"
		state.record.TimedOut = true
		state.record.Error = "timed out"
		if state.record.TerminationReason == "" {
			state.record.TerminationReason = ProcessTerminationOverallTimeout
		}
	case err != nil:
		state.record.Status = "failed"
		state.record.Error = strings.TrimSpace(err.Error())
		if state.record.TerminationReason == "" {
			state.record.TerminationReason = ProcessTerminationExit
		}
	default:
		state.record.Status = "completed"
		if state.record.TerminationReason == "" {
			state.record.TerminationReason = ProcessTerminationExit
		}
	}
	if code := exitCodeOf(err); code != nil {
		state.record.ExitCode = code
	}
	if signal := exitSignalOf(err); signal != "" {
		state.record.ExitSignal = signal
		if state.record.TerminationReason == "" {
			state.record.TerminationReason = ProcessTerminationSignal
		}
	}
	if state.record.TerminationReason == ProcessTerminationNoOutputTimeout {
		state.record.NoOutputTimedOut = true
		state.record.TimedOut = true
	}
	if state.record.Backgrounded {
		state.cleanupDeadline = now.Add(r.opts.CleanupTTL)
		delete(r.running, state.record.ID)
		r.finished[state.record.ID] = state
	} else {
		delete(r.running, state.record.ID)
		r.finished[state.record.ID] = state
	}
	close(state.done)
	state.cond.Broadcast()
}

func (r *ProcessRegistry) startSweeper() {
	r.sweeper = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-r.sweeper.C:
				r.pruneFinished()
			case <-r.stopSweep:
				r.sweeper.Stop()
				return
			}
		}
	}()
}

func (r *ProcessRegistry) pruneFinished() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for id, state := range r.finished {
		if state.record.Backgrounded && !state.cleanupDeadline.IsZero() && now.After(state.cleanupDeadline) {
			r.disposeStateLocked(state)
			delete(r.finished, id)
			continue
		}
		if !state.record.Backgrounded && state.exited {
			r.disposeStateLocked(state)
			delete(r.finished, id)
		}
	}
}

func (r *ProcessRegistry) armNoOutputTimer(state *processSessionState) {
	if state.noOutputWait <= 0 {
		return
	}
	if state.noOutputTimer != nil {
		state.noOutputTimer.Stop()
	}
	timer := time.AfterFunc(state.noOutputWait, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		current, ok := r.running[state.record.ID]
		if !ok || current.record.Status != "running" {
			return
		}
		current.record.State = "exiting"
		current.record.TerminationReason = ProcessTerminationNoOutputTimeout
		current.record.NoOutputTimedOut = true
		current.record.TimedOut = true
		current.killed = true
		cancel := current.cancel
		cmd := current.cmd
		ptyFile := current.ptyFile
		go func() {
			cancel()
			if ptyFile != nil {
				_ = ptyFile.Close()
			}
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}()
	})
	state.noOutputTimer = timer
}

func (r *ProcessRegistry) getState(sessionID string) (*processSessionState, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getStateLocked(strings.TrimSpace(sessionID))
}

func (r *ProcessRegistry) getStateLocked(sessionID string) (*processSessionState, bool) {
	if state, ok := r.running[sessionID]; ok {
		return state, true
	}
	if state, ok := r.finished[sessionID]; ok {
		return state, true
	}
	return nil, false
}

func (r *ProcessRegistry) disposeStateLocked(state *processSessionState) {
	if state.noOutputTimer != nil {
		state.noOutputTimer.Stop()
		state.noOutputTimer = nil
	}
	if state.stdin != nil {
		_ = state.stdin.Close()
		state.stdin = nil
	}
	if state.ptyFile != nil {
		_ = state.ptyFile.Close()
		state.ptyFile = nil
	}
}

func resolveProcessMaxOutput(value int, fallback int) int {
	if value > 0 {
		return value
	}
	if fallback > 0 {
		return fallback
	}
	return defaultProcessMaxOutputChars
}

func resolveProcessPendingOutput(value int, fallback int) int {
	if value > 0 {
		return value
	}
	if fallback > 0 {
		return fallback
	}
	return defaultProcessPendingOutputChars
}

func newID(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UTC().UnixNano())
}

func processID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func trimWithCap(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[len(value)-maxChars:]
}

func tailString(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxChars {
		return value
	}
	return value[len(value)-maxChars:]
}

func cloneProcessRecord(in ProcessSessionRecord) ProcessSessionRecord {
	out := in
	if in.EndedAt != nil {
		copyTime := *in.EndedAt
		out.EndedAt = &copyTime
	}
	return out
}

func exitCodeOf(err error) *int {
	if err == nil {
		code := 0
		return &code
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		return &code
	}
	return nil
}

func exitSignalOf(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	if exitErr.ProcessState == nil {
		return ""
	}
	return exitErr.ProcessState.String()
}

func platformSupportsPTY() bool {
	return runtime.GOOS != "windows"
}
