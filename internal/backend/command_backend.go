package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"

	"github.com/kocort/kocort/utils"
)

// CommandBackend executes an external command process as a backend.
type CommandBackend struct {
	Config core.CommandBackendConfig
	Env    *infra.EnvironmentRuntime
}

func (b *CommandBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	command := strings.TrimSpace(b.Config.Command)
	if command == "" {
		return core.AgentRunResult{}, fmt.Errorf("command backend requires command")
	}
	args := append([]string{}, b.Config.Args...)
	if b.Config.ModelArg != "" && runCtx.ModelSelection.Model != "" {
		args = append(args, b.Config.ModelArg, runCtx.ModelSelection.Model)
	}
	if b.Config.SessionArg != "" && strings.TrimSpace(runCtx.Session.SessionID) != "" {
		args = append(args, b.Config.SessionArg, runCtx.Session.SessionID)
	}
	if b.Config.SystemPromptArg != "" && strings.TrimSpace(runCtx.SystemPrompt) != "" {
		args = append(args, b.Config.SystemPromptArg, runCtx.SystemPrompt)
	}
	inputMode := b.Config.InputMode
	if inputMode == "" {
		inputMode = core.CommandBackendInputStdin
	}
	outputMode := b.Config.OutputMode
	if outputMode == "" {
		outputMode = core.CommandBackendOutputText
	}
	promptInput := buildCommandPromptInput(b.Config, runCtx)
	if inputMode == core.CommandBackendInputArg {
		if b.Config.PromptArg != "" {
			args = append(args, b.Config.PromptArg)
		}
		args = append(args, promptInput)
	}

	execCtx := ctx
	cancelExec := func() {}
	if b.Config.OverallTimeout > 0 {
		execCtx, cancelExec = context.WithTimeout(ctx, b.Config.OverallTimeout)
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline && runCtx.Request.Timeout > 0 {
		execCtx, cancelExec = context.WithTimeout(ctx, runCtx.Request.Timeout)
	} else {
		execCtx, cancelExec = context.WithCancel(ctx)
	}
	defer cancelExec()
	cmd := exec.CommandContext(execCtx, command, args...)
	if workdir := strings.TrimSpace(b.Config.WorkingDir); workdir != "" {
		cmd.Dir = workdir
	} else if workdir := strings.TrimSpace(runCtx.WorkspaceDir); workdir != "" {
		cmd.Dir = workdir
	}
	env := cmd.Environ()
	resolvedEnv, err := infra.AppendAgentRuntimeEnv(env, runCtx.Identity, b.Env, b.Config.Env)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	cmd.Env = resolvedEnv
	event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_started", "info", "command backend started", map[string]any{
		"command":    command,
		"args":       append([]string{}, args...),
		"inputMode":  inputMode,
		"outputMode": outputMode,
		"workingDir": cmd.Dir,
	})
	if inputMode == core.CommandBackendInputStdin {
		cmd.Stdin = strings.NewReader(promptInput)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return core.AgentRunResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if err := cmd.Start(); err != nil {
		event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_start_failed", "error", "command backend failed to start", map[string]any{
			"command": command,
			"error":   err.Error(),
		})
		return core.AgentRunResult{}, err
	}

	var watchdog *CommandOutputWatchdog
	if b.Config.NoOutputTimeout > 0 {
		watchdog = NewCommandOutputWatchdog(execCtx, b.Config.NoOutputTimeout, cancelExec)
		defer watchdog.Stop()
	}

	collector := &commandOutputCollector{
		runCtx:     runCtx,
		outputMode: outputMode,
		meta:       map[string]any{},
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		collector.readStdout(stdout, watchdog)
	}()
	go func() {
		defer wg.Done()
		collector.readStderr(stderr, watchdog)
	}()
	wg.Wait()
	waitErr := cmd.Wait()

	if watchdog != nil && watchdog.TimedOut() {
		event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_timed_out", "error", "command backend produced no output before timeout", map[string]any{
			"command": command,
		})
		return core.AgentRunResult{}, &BackendError{
			Reason:  BackendFailureTransientHTTP,
			Message: fmt.Sprintf("command backend produced no output for %s", b.Config.NoOutputTimeout.Round(time.Second)),
		}
	}
	if execCtx.Err() == context.DeadlineExceeded {
		event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_deadline_exceeded", "error", "command backend exceeded overall timeout", map[string]any{
			"command": command,
		})
		return core.AgentRunResult{}, &BackendError{
			Reason:  BackendFailureTransientHTTP,
			Message: "command backend exceeded overall timeout",
		}
	}
	if waitErr != nil {
		message := strings.TrimSpace(collector.stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_failed", "error", "command backend exited with error", map[string]any{
			"command": command,
			"error":   message,
		})
		return core.AgentRunResult{}, fmt.Errorf("%s", message)
	}
	result, err := collector.finish(b.Config)
	if err == nil {
		event.RecordModelEvent(execCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "command_completed", "info", "command backend completed", map[string]any{
			"command":    command,
			"stopReason": result.StopReason,
			"usage":      result.Usage,
		})
	}
	return result, err
}

type commandOutputCollector struct {
	mu         sync.Mutex
	runCtx     rtypes.AgentRunContext
	outputMode core.CommandBackendOutputMode
	stdout     bytes.Buffer
	stderr     bytes.Buffer
	payloads   []core.ReplyPayload
	usage      map[string]any
	meta       map[string]any
	events     []core.AgentEvent
	eventSeq   int
	stopReason string
	parseErr   error
}

func buildCommandPromptInput(cfg core.CommandBackendConfig, runCtx rtypes.AgentRunContext) string {
	message := runCtx.Request.Message
	systemPrompt := strings.TrimSpace(runCtx.SystemPrompt)
	if systemPrompt == "" {
		return message
	}
	if strings.TrimSpace(cfg.SystemPromptArg) != "" {
		return message
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SystemPromptMode)) {
	case "replace":
		return systemPrompt
	case "append":
		if strings.TrimSpace(message) == "" {
			return systemPrompt
		}
		return systemPrompt + "\n\n" + message
	default:
		return message
	}
}

func (c *commandOutputCollector) readStdout(reader io.Reader, watchdog *CommandOutputWatchdog) {
	switch c.outputMode {
	case core.CommandBackendOutputJSONL:
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if watchdog != nil {
				watchdog.Touch()
			}
			event.RecordModelEvent(context.Background(), c.runCtx.Runtime.GetAudit(), nil, c.runCtx.Identity.ID, c.runCtx.Session.SessionKey, c.runCtx.Request.RunID, "command_stdout_line", "debug", "command backend emitted stdout jsonl line", map[string]any{
				"line": line,
			})
			c.handleJSONLLine(line)
			c.mu.Lock()
			failed := c.parseErr != nil
			c.mu.Unlock()
			if failed {
				return
			}
		}
	default:
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				if watchdog != nil {
					watchdog.Touch()
				}
				c.mu.Lock()
				_, _ = c.stdout.Write(buf[:n]) // best-effort; buffer write rarely fails
				c.mu.Unlock()
				event.RecordModelEvent(context.Background(), c.runCtx.Runtime.GetAudit(), nil, c.runCtx.Identity.ID, c.runCtx.Session.SessionKey, c.runCtx.Request.RunID, "command_stdout_chunk", "debug", "command backend emitted stdout chunk", map[string]any{
					"text": string(buf[:n]),
				})
			}
			if err != nil {
				return
			}
		}
	}
}

func (c *commandOutputCollector) readStderr(reader io.Reader, watchdog *CommandOutputWatchdog) {
	buf := make([]byte, 2048)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if watchdog != nil {
				watchdog.Touch()
			}
			c.mu.Lock()
			_, _ = c.stderr.Write(buf[:n]) // best-effort; buffer write rarely fails
			c.mu.Unlock()
			event.RecordModelEvent(context.Background(), c.runCtx.Runtime.GetAudit(), nil, c.runCtx.Identity.ID, c.runCtx.Session.SessionKey, c.runCtx.Request.RunID, "command_stderr_chunk", "debug", "command backend emitted stderr chunk", map[string]any{
				"text": string(buf[:n]),
			})
		}
		if err != nil {
			return
		}
	}
}

func (c *commandOutputCollector) handleJSONLLine(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		c.mu.Lock()
		c.parseErr = err
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.usage == nil {
		c.usage = map[string]any{}
	}
	if usageValue, ok := event["usage"]; ok {
		if usageMap, ok := usageValue.(map[string]any); ok {
			for key, value := range usageMap {
				c.usage[key] = value
			}
		}
	}
	if sessionID := ExtractSessionIDFromMap(event, nil); sessionID != "" {
		c.usage["sessionId"] = sessionID
	}
	if stopReason := strings.TrimSpace(AsString(event["stopReason"])); stopReason != "" {
		c.stopReason = stopReason
	}
	if eventType := strings.TrimSpace(AsString(event["type"])); eventType == "error" {
		message := strings.TrimSpace(AsString(event["text"]))
		if message == "" {
			message = strings.TrimSpace(AsString(event["message"]))
		}
		c.parseErr = fmt.Errorf("%s", utils.NonEmpty(message, "command backend emitted error event"))
		return
	}
	if eventType := strings.TrimSpace(AsString(event["type"])); eventType == "status" {
		c.appendEvent("status", map[string]any{
			"type": "status",
			"used": event["used"],
			"size": event["size"],
		})
		if used, ok := event["used"]; ok {
			c.usage["used"] = used
		}
		if size, ok := event["size"]; ok {
			c.usage["size"] = size
		}
		return
	}
	text := strings.TrimSpace(AsString(event["text"]))
	if text == "" {
		return
	}
	payload := core.ReplyPayload{Text: text}
	switch strings.TrimSpace(AsString(event["type"])) {
	case "tool_call":
		c.appendEvent("tool", map[string]any{
			"type": "tool_call",
			"text": text,
		})
		c.runCtx.ReplyDispatcher.SendToolResult(payload)
	case "final":
		c.appendEvent("assistant", map[string]any{
			"type": "final",
			"text": text,
		})
		c.runCtx.ReplyDispatcher.SendFinalReply(payload)
	default:
		if AsBool(event["final"]) {
			c.appendEvent("assistant", map[string]any{
				"type": "final",
				"text": text,
			})
			c.runCtx.ReplyDispatcher.SendFinalReply(payload)
		} else {
			c.appendEvent("assistant", map[string]any{
				"type": "text_delta",
				"text": text,
			})
			c.runCtx.ReplyDispatcher.SendBlockReply(payload)
		}
	}
	c.payloads = append(c.payloads, payload)
}

func (c *commandOutputCollector) appendEvent(stream string, data map[string]any) {
	c.eventSeq++
	agentEvent := core.AgentEvent{
		RunID:      strings.TrimSpace(c.runCtx.Request.RunID),
		Seq:        c.eventSeq,
		Stream:     strings.TrimSpace(stream),
		OccurredAt: time.Now().UTC(),
		SessionKey: strings.TrimSpace(c.runCtx.Session.SessionKey),
		Data:       CloneAnyMap(data),
	}
	c.events = append(c.events, agentEvent)
	event.EmitAgentEvent(c.runCtx.Runtime.GetEventBus(), agentEvent)
}

func (c *commandOutputCollector) finish(cfg core.CommandBackendConfig) (core.AgentRunResult, error) {
	switch c.outputMode {
	case core.CommandBackendOutputJSON:
		result, err := ParseCommandJSONOutput(c.stdout.Bytes(), cfg)
		if err != nil {
			return core.AgentRunResult{}, err
		}
		for i, payload := range result.Payloads {
			if strings.TrimSpace(payload.Text) == "" {
				continue
			}
			if i == len(result.Payloads)-1 {
				c.appendEvent("assistant", map[string]any{
					"type": "final",
					"text": payload.Text,
				})
				c.runCtx.ReplyDispatcher.SendFinalReply(payload)
			} else {
				c.appendEvent("assistant", map[string]any{
					"type": "text_delta",
					"text": payload.Text,
				})
				c.runCtx.ReplyDispatcher.SendBlockReply(payload)
			}
		}
		if result.Meta == nil {
			result.Meta = map[string]any{}
		}
		result.Meta["backendKind"] = "command"
		result.Meta["terminationReason"] = utils.NonEmpty(strings.TrimSpace(result.StopReason), "exit")
		result.Events = append([]core.AgentEvent{}, c.events...)
		return result, nil
	case core.CommandBackendOutputJSONL:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.parseErr != nil {
			return core.AgentRunResult{}, c.parseErr
		}
		return core.AgentRunResult{
			Payloads: append([]core.ReplyPayload{}, c.payloads...),
			Events:   append([]core.AgentEvent{}, c.events...),
			Usage:    CloneAnyMap(c.usage),
			Meta: map[string]any{
				"backendKind":       "command",
				"terminationReason": utils.NonEmpty(strings.TrimSpace(c.stopReason), "exit"),
			},
			StopReason: c.stopReason,
		}, nil
	default:
		c.mu.Lock()
		text := strings.TrimSpace(c.stdout.String())
		c.mu.Unlock()
		if text != "" {
			if cfg.StreamText {
				c.appendEvent("assistant", map[string]any{
					"type": "text_delta",
					"text": text,
				})
				c.runCtx.ReplyDispatcher.SendBlockReply(core.ReplyPayload{Text: text})
			}
			c.appendEvent("assistant", map[string]any{
				"type": "final",
				"text": text,
			})
			c.runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: text})
			return core.AgentRunResult{
				Payloads: []core.ReplyPayload{{Text: text}},
				Events:   append([]core.AgentEvent{}, c.events...),
				Meta: map[string]any{
					"backendKind":       "command",
					"terminationReason": "exit",
				},
			}, nil
		}
		return core.AgentRunResult{}, nil
	}
}
