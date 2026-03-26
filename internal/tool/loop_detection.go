package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

const (
	defaultToolCallHistorySize             = 30
	defaultToolLoopWarningThreshold        = 10
	defaultToolLoopCriticalThreshold       = 20
	defaultToolLoopCircuitBreakerThreshold = 30
)

// ToolLoopDetectorKind identifies the type of loop detector that triggered.
type ToolLoopDetectorKind string

const (
	ToolLoopDetectorGenericRepeat        ToolLoopDetectorKind = "generic_repeat"
	ToolLoopDetectorKnownPollNoProgress  ToolLoopDetectorKind = "known_poll_no_progress"
	ToolLoopDetectorGlobalCircuitBreaker ToolLoopDetectorKind = "global_circuit_breaker"
	ToolLoopDetectorPingPong             ToolLoopDetectorKind = "ping_pong"
)

// ToolLoopDetectionResult holds the outcome of a loop detection check.
type ToolLoopDetectionResult struct {
	Stuck          bool
	Level          string
	Detector       ToolLoopDetectorKind
	Count          int
	Message        string
	PairedToolName string
	WarningKey     string
}

type resolvedToolLoopDetectionConfig struct {
	Enabled                       bool
	HistorySize                   int
	WarningThreshold              int
	CriticalThreshold             int
	GlobalCircuitBreakerThreshold int
	GenericRepeat                 bool
	KnownPollNoProgress           bool
	PingPong                      bool
}

// ToolLoopHistoryEntry records a single tool call in the loop detection history.
type ToolLoopHistoryEntry struct {
	ToolName   string
	ArgsHash   string
	ToolCallID string
	ResultHash string
	Timestamp  int64
}

// ToolLoopSessionState tracks loop detection state for a session.
type ToolLoopSessionState struct {
	History        []ToolLoopHistoryEntry
	WarningBuckets map[string]int
}

// ToolLoopRegistry manages per-session tool loop detection state.
type ToolLoopRegistry struct {
	mu       sync.Mutex
	sessions map[string]*ToolLoopSessionState
}

// NewToolLoopRegistry creates a new ToolLoopRegistry.
func NewToolLoopRegistry() *ToolLoopRegistry {
	return &ToolLoopRegistry{sessions: map[string]*ToolLoopSessionState{}}
}

// Get returns (or creates) the loop state for the given session.
func (r *ToolLoopRegistry) Get(sessionKey, sessionID string) *ToolLoopSessionState {
	if r == nil {
		return nil
	}
	key := toolLoopStateKey(sessionKey, sessionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.sessions[key]
	if state == nil {
		state = &ToolLoopSessionState{WarningBuckets: map[string]int{}}
		r.sessions[key] = state
	}
	return state
}

func toolLoopStateKey(sessionKey, sessionID string) string {
	return strings.TrimSpace(sessionKey) + "|" + strings.TrimSpace(sessionID)
}

func resolveToolLoopDetectionConfig(config core.ToolLoopDetectionConfig) resolvedToolLoopDetectionConfig {
	warning := positiveInt(config.WarningThreshold, defaultToolLoopWarningThreshold)
	critical := positiveInt(config.CriticalThreshold, defaultToolLoopCriticalThreshold)
	global := positiveInt(config.GlobalCircuitBreakerThreshold, defaultToolLoopCircuitBreakerThreshold)
	if critical <= warning {
		critical = warning + 1
	}
	if global <= critical {
		global = critical + 1
	}
	return resolvedToolLoopDetectionConfig{
		Enabled:                       config.Enabled != nil && *config.Enabled,
		HistorySize:                   positiveInt(config.HistorySize, defaultToolCallHistorySize),
		WarningThreshold:              warning,
		CriticalThreshold:             critical,
		GlobalCircuitBreakerThreshold: global,
		GenericRepeat:                 boolWithDefault(config.Detectors.GenericRepeat, true),
		KnownPollNoProgress:           boolWithDefault(config.Detectors.KnownPollNoProgress, true),
		PingPong:                      boolWithDefault(config.Detectors.PingPong, true),
	}
}

func positiveInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func boolWithDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func hashToolCall(toolName string, params any) string {
	return NormalizeToolPolicyName(toolName) + ":" + digestStable(params)
}

func digestStable(value any) string {
	data, err := stableMarshal(value)
	if err != nil {
		return fmt.Sprintf("%T:%v", value, value)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stableMarshal(value any) ([]byte, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make([]any, 0, len(keys)*2)
		for _, key := range keys {
			child, err := stableMarshal(typed[key])
			if err != nil {
				return nil, err
			}
			ordered = append(ordered, key, string(child))
		}
		return json.Marshal(ordered)
	case []any:
		ordered := make([]string, 0, len(typed))
		for _, item := range typed {
			child, err := stableMarshal(item)
			if err != nil {
				return nil, err
			}
			ordered = append(ordered, string(child))
		}
		return json.Marshal(ordered)
	default:
		return json.Marshal(value)
	}
}

func isKnownPollToolCall(toolName string, params map[string]any) bool {
	if NormalizeToolPolicyName(toolName) != "process" {
		return false
	}
	action, _ := params["action"].(string) // zero value fallback is intentional
	action = strings.ToLower(strings.TrimSpace(action))
	return action == "poll" || action == "log"
}

func resolveToolLoopResultHash(toolName string, params map[string]any, result core.ToolResult, err error) string {
	if err != nil {
		return "error:" + digestStable(err.Error())
	}
	if len(result.JSON) > 0 {
		return digestStable(json.RawMessage(result.JSON))
	}
	if strings.TrimSpace(result.Text) != "" {
		return digestStable(strings.TrimSpace(result.Text))
	}
	if isKnownPollToolCall(toolName, params) {
		return digestStable("{}")
	}
	return ""
}

func getNoProgressStreak(history []ToolLoopHistoryEntry, toolName, argsHash string) (int, string) {
	latest := ""
	streak := 0
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if entry.ToolName != toolName || entry.ArgsHash != argsHash || strings.TrimSpace(entry.ResultHash) == "" {
			continue
		}
		if latest == "" {
			latest = entry.ResultHash
			streak = 1
			continue
		}
		if entry.ResultHash != latest {
			break
		}
		streak++
	}
	return streak, latest
}

func getPingPongStreak(history []ToolLoopHistoryEntry, currentSignature string) (count int, pairedToolName, pairedSignature string, noProgress bool) {
	if len(history) == 0 {
		return 0, "", "", false
	}
	last := history[len(history)-1]
	for i := len(history) - 2; i >= 0; i-- {
		call := history[i]
		if call.ArgsHash == last.ArgsHash {
			continue
		}
		pairedSignature = call.ArgsHash
		pairedToolName = call.ToolName
		break
	}
	if pairedSignature == "" || currentSignature != pairedSignature {
		return 0, "", "", false
	}
	alternatingTailCount := 0
	for i := len(history) - 1; i >= 0; i-- {
		call := history[i]
		expected := last.ArgsHash
		if alternatingTailCount%2 == 1 {
			expected = pairedSignature
		}
		if call.ArgsHash != expected {
			break
		}
		alternatingTailCount++
	}
	if alternatingTailCount < 2 {
		return 0, "", "", false
	}
	tailStart := len(history) - alternatingTailCount
	hashA, hashB := "", ""
	noProgress = true
	for i := tailStart; i < len(history); i++ {
		call := history[i]
		if strings.TrimSpace(call.ResultHash) == "" {
			noProgress = false
			break
		}
		if call.ArgsHash == last.ArgsHash {
			if hashA == "" {
				hashA = call.ResultHash
			} else if hashA != call.ResultHash {
				noProgress = false
				break
			}
			continue
		}
		if call.ArgsHash == pairedSignature {
			if hashB == "" {
				hashB = call.ResultHash
			} else if hashB != call.ResultHash {
				noProgress = false
				break
			}
			continue
		}
		noProgress = false
		break
	}
	if hashA == "" || hashB == "" {
		noProgress = false
	}
	return alternatingTailCount + 1, pairedToolName, pairedSignature, noProgress
}

func canonicalPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// DetectToolCallLoop checks the session history for loop patterns.
func DetectToolCallLoop(state *ToolLoopSessionState, toolName string, params map[string]any, config core.ToolLoopDetectionConfig) ToolLoopDetectionResult {
	resolved := resolveToolLoopDetectionConfig(config)
	if !resolved.Enabled || state == nil {
		return ToolLoopDetectionResult{}
	}
	currentHash := hashToolCall(toolName, params)
	noProgressStreak, latestResultHash := getNoProgressStreak(state.History, NormalizeToolPolicyName(toolName), currentHash)
	knownPollTool := isKnownPollToolCall(toolName, params)
	pingPongCount, pairedToolName, pairedSignature, pingPongNoProgress := getPingPongStreak(state.History, currentHash)

	if noProgressStreak >= resolved.GlobalCircuitBreakerThreshold {
		return ToolLoopDetectionResult{
			Stuck:      true,
			Level:      "critical",
			Detector:   ToolLoopDetectorGlobalCircuitBreaker,
			Count:      noProgressStreak,
			Message:    fmt.Sprintf("CRITICAL: %s has repeated identical no-progress outcomes %d times. Session execution blocked by global circuit breaker to prevent runaway loops.", NormalizeToolPolicyName(toolName), noProgressStreak),
			WarningKey: "global:" + NormalizeToolPolicyName(toolName) + ":" + currentHash + ":" + utils.NonEmpty(latestResultHash, "none"),
		}
	}
	if knownPollTool && resolved.KnownPollNoProgress && noProgressStreak >= resolved.CriticalThreshold {
		return ToolLoopDetectionResult{
			Stuck:      true,
			Level:      "critical",
			Detector:   ToolLoopDetectorKnownPollNoProgress,
			Count:      noProgressStreak,
			Message:    fmt.Sprintf("CRITICAL: Called %s with identical arguments and no progress %d times. This appears to be a stuck polling loop. Session execution blocked to prevent resource waste.", NormalizeToolPolicyName(toolName), noProgressStreak),
			WarningKey: "poll:" + NormalizeToolPolicyName(toolName) + ":" + currentHash + ":" + utils.NonEmpty(latestResultHash, "none"),
		}
	}
	if knownPollTool && resolved.KnownPollNoProgress && noProgressStreak >= resolved.WarningThreshold {
		return ToolLoopDetectionResult{
			Stuck:      true,
			Level:      "warning",
			Detector:   ToolLoopDetectorKnownPollNoProgress,
			Count:      noProgressStreak,
			Message:    fmt.Sprintf("WARNING: You have called %s %d times with identical arguments and no progress. Stop polling and either increase wait time between checks or report the task as failed.", NormalizeToolPolicyName(toolName), noProgressStreak),
			WarningKey: "poll:" + NormalizeToolPolicyName(toolName) + ":" + currentHash + ":" + utils.NonEmpty(latestResultHash, "none"),
		}
	}
	pingPongWarningKey := "pingpong:" + NormalizeToolPolicyName(toolName) + ":" + currentHash
	if pairedSignature != "" {
		pingPongWarningKey = "pingpong:" + canonicalPairKey(currentHash, pairedSignature)
	}
	if resolved.PingPong && pingPongCount >= resolved.CriticalThreshold && pingPongNoProgress {
		return ToolLoopDetectionResult{
			Stuck:          true,
			Level:          "critical",
			Detector:       ToolLoopDetectorPingPong,
			Count:          pingPongCount,
			Message:        fmt.Sprintf("CRITICAL: You are alternating between repeated tool-call patterns (%d consecutive calls) with no progress. This appears to be a stuck ping-pong loop. Session execution blocked to prevent resource waste.", pingPongCount),
			PairedToolName: pairedToolName,
			WarningKey:     pingPongWarningKey,
		}
	}
	if resolved.PingPong && pingPongCount >= resolved.WarningThreshold {
		return ToolLoopDetectionResult{
			Stuck:          true,
			Level:          "warning",
			Detector:       ToolLoopDetectorPingPong,
			Count:          pingPongCount,
			Message:        fmt.Sprintf("WARNING: You are alternating between repeated tool-call patterns (%d consecutive calls). This looks like a ping-pong loop; stop retrying and report the task as failed.", pingPongCount),
			PairedToolName: pairedToolName,
			WarningKey:     pingPongWarningKey,
		}
	}
	recentCount := 0
	for _, entry := range state.History {
		if entry.ToolName == NormalizeToolPolicyName(toolName) && entry.ArgsHash == currentHash {
			recentCount++
		}
	}
	if !knownPollTool && resolved.GenericRepeat && recentCount >= resolved.WarningThreshold {
		return ToolLoopDetectionResult{
			Stuck:      true,
			Level:      "warning",
			Detector:   ToolLoopDetectorGenericRepeat,
			Count:      recentCount,
			Message:    fmt.Sprintf("WARNING: You have called %s %d times with identical arguments. If this is not making progress, stop retrying and report the task as failed.", NormalizeToolPolicyName(toolName), recentCount),
			WarningKey: "generic:" + NormalizeToolPolicyName(toolName) + ":" + currentHash,
		}
	}
	return ToolLoopDetectionResult{}
}

// RecordToolCall records a tool call in the session loop detection history.
func RecordToolCall(state *ToolLoopSessionState, toolName string, params map[string]any, toolCallID string, config core.ToolLoopDetectionConfig) {
	if state == nil {
		return
	}
	resolved := resolveToolLoopDetectionConfig(config)
	state.History = append(state.History, ToolLoopHistoryEntry{
		ToolName:   NormalizeToolPolicyName(toolName),
		ArgsHash:   hashToolCall(toolName, params),
		ToolCallID: strings.TrimSpace(toolCallID),
		Timestamp:  time.Now().UnixMilli(),
	})
	if len(state.History) > resolved.HistorySize {
		state.History = state.History[len(state.History)-resolved.HistorySize:]
	}
}

// RecordToolCallOutcome records the result hash for a previously recorded tool call.
func RecordToolCallOutcome(state *ToolLoopSessionState, toolName string, params map[string]any, toolCallID string, result core.ToolResult, err error, config core.ToolLoopDetectionConfig) {
	if state == nil {
		return
	}
	resolved := resolveToolLoopDetectionConfig(config)
	argsHash := hashToolCall(toolName, params)
	resultHash := resolveToolLoopResultHash(toolName, params, result, err)
	if resultHash == "" {
		return
	}
	matched := false
	for i := len(state.History) - 1; i >= 0; i-- {
		entry := &state.History[i]
		if strings.TrimSpace(toolCallID) != "" && entry.ToolCallID != strings.TrimSpace(toolCallID) {
			continue
		}
		if entry.ToolName != NormalizeToolPolicyName(toolName) || entry.ArgsHash != argsHash || strings.TrimSpace(entry.ResultHash) != "" {
			continue
		}
		entry.ResultHash = resultHash
		matched = true
		break
	}
	if !matched {
		state.History = append(state.History, ToolLoopHistoryEntry{
			ToolName:   NormalizeToolPolicyName(toolName),
			ArgsHash:   argsHash,
			ToolCallID: strings.TrimSpace(toolCallID),
			ResultHash: resultHash,
			Timestamp:  time.Now().UnixMilli(),
		})
	}
	if len(state.History) > resolved.HistorySize {
		state.History = state.History[len(state.History)-resolved.HistorySize:]
	}
}

// ShouldEmitToolLoopWarning returns true if a warning event should be emitted for the given key/count.
func ShouldEmitToolLoopWarning(state *ToolLoopSessionState, warningKey string, count int) bool {
	if state == nil || strings.TrimSpace(warningKey) == "" {
		return false
	}
	if state.WarningBuckets == nil {
		state.WarningBuckets = map[string]int{}
	}
	const loopWarningBucketSize = 10
	const maxLoopWarningKeys = 256
	bucket := count / loopWarningBucketSize
	lastBucket := state.WarningBuckets[warningKey]
	if bucket <= lastBucket {
		return false
	}
	state.WarningBuckets[warningKey] = bucket
	if len(state.WarningBuckets) > maxLoopWarningKeys {
		for key := range state.WarningBuckets {
			delete(state.WarningBuckets, key)
			break
		}
	}
	return true
}
