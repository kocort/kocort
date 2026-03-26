package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

const (
	defaultProcessMaxOutputChars = 200_000
	defaultProcessTailChars      = 4_000
)

// ProcessSessionRecord describes a managed process session.
type ProcessSessionRecord struct {
	ID           string     `json:"id"`
	SessionKey   string     `json:"sessionKey,omitempty"`
	RunID        string     `json:"runId,omitempty"`
	Command      string     `json:"command"`
	Workdir      string     `json:"workdir,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	Status       string     `json:"status"`
	Backgrounded bool       `json:"backgrounded,omitempty"`
	PID          int        `json:"pid,omitempty"`
	ExitCode     *int       `json:"exitCode,omitempty"`
	TimedOut     bool       `json:"timedOut,omitempty"`
	Error        string     `json:"error,omitempty"`
	Tail         string     `json:"tail,omitempty"`
	Output       string     `json:"output,omitempty"`
}

// ProcessRegistry manages running processes.
type ProcessRegistry struct {
	mu       sync.Mutex
	sessions map[string]*processSessionState
}

type processSessionState struct {
	record    ProcessSessionRecord
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	done      chan struct{}
	killed    bool
	output    string
	outputSeq int64
	waitErr   error
	maxOutput int
	cond      *sync.Cond
}

// NewProcessRegistry creates a new ProcessRegistry.
func NewProcessRegistry() *ProcessRegistry {
	return &ProcessRegistry{sessions: map[string]*processSessionState{}}
}

// Start launches a new managed process.
func (r *ProcessRegistry) Start(ctx context.Context, opts ProcessStartOptions) (ProcessSessionRecord, error) {
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
	cmd, err := infra.CommandShellContext(procCtx, command)
	if err != nil {
		cancel()
		return ProcessSessionRecord{}, err
	}
	if strings.TrimSpace(opts.Workdir) != "" {
		cmd.Dir = strings.TrimSpace(opts.Workdir)
	}
	if len(opts.Env) > 0 {
		cmd.Env = append([]string{}, opts.Env...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return ProcessSessionRecord{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return ProcessSessionRecord{}, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return ProcessSessionRecord{}, err
	}
	now := time.Now().UTC()
	state := &processSessionState{
		record: ProcessSessionRecord{
			ID:           newID("proc_"),
			SessionKey:   strings.TrimSpace(opts.SessionKey),
			RunID:        strings.TrimSpace(opts.RunID),
			Command:      command,
			Workdir:      strings.TrimSpace(opts.Workdir),
			StartedAt:    now,
			Status:       "running",
			Backgrounded: opts.Backgrounded,
			PID:          processID(cmd),
		},
		cancel:    cancel,
		cmd:       cmd,
		done:      make(chan struct{}),
		maxOutput: max(defaultProcessMaxOutputChars, opts.MaxOutputChars),
	}
	state.cond = sync.NewCond(&r.mu)
	r.mu.Lock()
	r.sessions[state.record.ID] = state
	r.mu.Unlock()

	go r.captureOutput(state, stdout)
	go r.captureOutput(state, stderr)
	go r.wait(state, procCtx)

	return cloneProcessRecord(state.record), nil
}

// List returns all process records.
func (r *ProcessRegistry) List() []ProcessSessionRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProcessSessionRecord, 0, len(r.sessions))
	for _, state := range r.sessions {
		out = append(out, cloneProcessRecord(state.record))
	}
	return out
}

// Get returns the record for a specific process.
func (r *ProcessRegistry) Get(sessionID string) (ProcessSessionRecord, bool) {
	if r == nil {
		return ProcessSessionRecord{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return ProcessSessionRecord{}, false
	}
	return cloneProcessRecord(state.record), true
}

// Poll waits up to the specified duration for a process to complete.
func (r *ProcessRegistry) Poll(sessionID string, wait time.Duration) (ProcessSessionRecord, bool) {
	if r == nil {
		return ProcessSessionRecord{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ProcessSessionRecord{}, false
	}
	deadline := time.Now().Add(wait)
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.sessions[sessionID]
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
	}
	return cloneProcessRecord(state.record), true
}

// Kill terminates a running process.
func (r *ProcessRegistry) Kill(sessionID string) (ProcessSessionRecord, bool, error) {
	if r == nil {
		return ProcessSessionRecord{}, false, core.ErrProcessNotConfigured
	}
	sessionID = strings.TrimSpace(sessionID)
	r.mu.Lock()
	state, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return ProcessSessionRecord{}, false, nil
	}
	state.killed = true
	cancel := state.cancel
	cmd := state.cmd
	r.mu.Unlock()

	cancel()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill() // best-effort; process may already be dead
	}
	record, _ := r.Poll(sessionID, 250*time.Millisecond) // zero value fallback is intentional
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
	if strings.TrimSpace(chunk) == "" && chunk == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state.output += chunk
	if len(state.output) > state.maxOutput {
		state.output = state.output[len(state.output)-state.maxOutput:]
	}
	state.record.Output = state.output
	state.record.Tail = tailString(state.output, defaultProcessTailChars)
	state.outputSeq++
	state.cond.Broadcast()
}

func (r *ProcessRegistry) wait(state *processSessionState, procCtx context.Context) {
	err := state.cmd.Wait()
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	state.waitErr = err
	state.record.EndedAt = &now
	state.record.Output = state.output
	state.record.Tail = tailString(state.output, defaultProcessTailChars)
	switch {
	case state.killed || errors.Is(procCtx.Err(), context.Canceled):
		state.record.Status = "killed"
		state.record.Error = "killed"
	case errors.Is(procCtx.Err(), context.DeadlineExceeded):
		state.record.Status = "timed_out"
		state.record.TimedOut = true
		state.record.Error = "timed out"
	case err != nil:
		state.record.Status = "failed"
		state.record.Error = strings.TrimSpace(err.Error())
	default:
		state.record.Status = "completed"
	}
	if code := exitCodeOf(err); code != nil {
		state.record.ExitCode = code
	}
	close(state.done)
	state.cond.Broadcast()
}

// ProcessStartOptions configures a new process start.
type ProcessStartOptions struct {
	SessionKey     string
	RunID          string
	Command        string
	Workdir        string
	Env            []string
	Timeout        time.Duration
	Backgrounded   bool
	MaxOutputChars int
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
