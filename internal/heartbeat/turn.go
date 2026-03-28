package heartbeat

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
)

type TurnPlan struct {
	Skip           bool
	SkipReason     string
	Prompt         string
	Deliver        bool
	DeliveryTarget DeliveryTargetPlan
	Visibility     Visibility
	RunSessionKey  string
	IsolatedRun    bool
	Model          string
	AckMaxChars    int
	InternalEvents []core.TranscriptMessage
}

type TurnPlanInput struct {
	Identity             core.AgentIdentity
	Request              HeartbeatWakeRequest
	Config               config.AppConfig
	HeartbeatFileContent string
	HeartbeatFileExists  bool
	WorkspaceDir         string
	Events               []infra.SystemEvent
	SessionBusy          bool
	QueueDepth           int
	Now                  time.Time
	SessionEntry         *core.SessionEntry
}

func BuildTurnPlan(input TurnPlanInput) TurnPlan {
	if input.SessionBusy || input.QueueDepth > 0 {
		return TurnPlan{
			Skip:       true,
			SkipReason: "requests-in-flight",
		}
	}
	if !IsWithinActiveHours(input.Identity, input.Now) {
		return TurnPlan{
			Skip:       true,
			SkipReason: "quiet-hours",
		}
	}
	events := buildInternalEvents(input.Events)
	heartbeatFile := strings.TrimSpace(input.HeartbeatFileContent)
	if shouldSkipForEmptyHeartbeat(input.Request, events, heartbeatFile, input.HeartbeatFileExists) {
		return TurnPlan{
			Skip:       true,
			SkipReason: "no-events",
		}
	}

	target := ResolveDeliveryTarget(input.Config, input.Identity, input.SessionEntry)
	visibility := ResolveVisibility(input.Config, target.Channel, target.AccountID)
	if !visibility.ShowAlerts && !visibility.ShowOK && !visibility.UseIndicator {
		return TurnPlan{
			Skip:           true,
			SkipReason:     "alerts-disabled",
			DeliveryTarget: target,
			Visibility:     visibility,
		}
	}
	deliver := target.Enabled && shouldDeliverHeartbeat(input.Identity, input.Request)
	runSessionKey, isolatedRun := resolveHeartbeatRunSession(input.Identity, input.Request.SessionKey, input.Config)
	prompt := ResolveHeartbeatPrompt(input.Identity.HeartbeatPrompt)
	if events.hasExecCompletion {
		prompt = BuildExecEventPrompt(deliver)
	} else if len(events.cronTexts) > 0 {
		prompt = BuildCronEventPrompt(events.cronTexts, deliver)
	}
	prompt = appendHeartbeatWorkspacePathHint(prompt, input.WorkspaceDir)

	return TurnPlan{
		Prompt:         prompt,
		Deliver:        deliver,
		DeliveryTarget: target,
		Visibility:     visibility,
		RunSessionKey:  runSessionKey,
		IsolatedRun:    isolatedRun,
		Model:          strings.TrimSpace(input.Identity.HeartbeatModel),
		AckMaxChars:    input.Identity.HeartbeatAckMaxChars,
		InternalEvents: events.messages,
	}
}

func shouldSkipForEmptyHeartbeat(req HeartbeatWakeRequest, events internalEventSet, heartbeatFile string, heartbeatFileExists bool) bool {
	if len(events.messages) > 0 {
		return false
	}
	if !heartbeatFileExists {
		return false
	}
	if IsHeartbeatContentEffectivelyEmpty(heartbeatFile) {
		reason := strings.ToLower(strings.TrimSpace(req.Reason))
		if strings.HasPrefix(reason, "cron:") || reason == "wake" || reason == "hook" || reason == "exec-event" {
			return false
		}
		return true
	}
	return false
}

type internalEventSet struct {
	messages          []core.TranscriptMessage
	texts             []string
	cronTexts         []string
	hasExecCompletion bool
}

func buildInternalEvents(events []infra.SystemEvent) internalEventSet {
	set := internalEventSet{
		messages: make([]core.TranscriptMessage, 0, len(events)),
		texts:    make([]string, 0, len(events)),
	}
	for _, item := range events {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		set.texts = append(set.texts, text)
		set.messages = append(set.messages, core.TranscriptMessage{
			Type:      "system_event",
			Role:      "system",
			Text:      text,
			Timestamp: item.Timestamp,
			Event:     "heartbeat",
		})
		if IsExecCompletionEvent(text) {
			set.hasExecCompletion = true
			continue
		}
		if isCronReason(item.ContextKey) && IsCronSystemEvent(text) {
			set.cronTexts = append(set.cronTexts, text)
		}
	}
	return set
}

func shouldDeliverHeartbeat(identity core.AgentIdentity, req HeartbeatWakeRequest) bool {
	target := strings.TrimSpace(identity.HeartbeatTarget)
	if target != "" && !strings.EqualFold(target, "none") {
		return true
	}
	reason := strings.TrimSpace(req.Reason)
	if strings.TrimSpace(req.SessionKey) == "" {
		return false
	}
	return isCronReason(reason) || strings.EqualFold(reason, "wake")
}

func isCronReason(reason string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(reason)), "cron:")
}

func appendHeartbeatWorkspacePathHint(prompt, workspaceDir string) string {
	if !strings.Contains(strings.ToLower(prompt), "heartbeat.md") {
		return prompt
	}
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return prompt
	}
	heartbeatPath := filepath.ToSlash(filepath.Join(workspaceDir, "HEARTBEAT.md"))
	hint := "When reading HEARTBEAT.md, use workspace file " + heartbeatPath + " (exact case)."
	if strings.Contains(prompt, hint) {
		return prompt
	}
	return prompt + "\n" + hint
}

func resolveHeartbeatRunSession(identity core.AgentIdentity, sessionKey string, cfg config.AppConfig) (string, bool) {
	sessionKey = resolveHeartbeatBaseSession(identity, sessionKey, cfg)
	if sessionKey == "" {
		return "", false
	}
	if !identity.HeartbeatIsolatedSession {
		return sessionKey, false
	}
	return sessionKey + ":heartbeat", true
}

func resolveHeartbeatBaseSession(identity core.AgentIdentity, sessionKey string, cfg config.AppConfig) string {
	if trimmed := strings.TrimSpace(identity.HeartbeatSession); trimmed != "" {
		if strings.EqualFold(trimmed, "main") {
			return session.BuildMainSessionKeyWithMain(identity.ID, config.ResolveSessionMainKey(cfg))
		}
		return trimmed
	}
	return strings.TrimSpace(sessionKey)
}

func ResolveHeartbeatSessionKeyForRuntime(identity core.AgentIdentity, sessionKey string, cfg config.AppConfig) string {
	return resolveHeartbeatBaseSession(identity, sessionKey, cfg)
}
