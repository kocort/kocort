package tool

import (
	"fmt"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func boolPtr(v bool) *bool { return &v }

func enabledConfig() core.ToolLoopDetectionConfig {
	return core.ToolLoopDetectionConfig{
		Enabled:                       boolPtr(true),
		HistorySize:                   30,
		WarningThreshold:              3,
		CriticalThreshold:             5,
		GlobalCircuitBreakerThreshold: 8,
	}
}

// ---------------------------------------------------------------------------
// NewToolLoopRegistry
// ---------------------------------------------------------------------------

func TestNewToolLoopRegistry(t *testing.T) {
	r := NewToolLoopRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestToolLoopRegistryGet(t *testing.T) {
	r := NewToolLoopRegistry()
	state1 := r.Get("key1", "id1")
	if state1 == nil {
		t.Fatal("expected non-nil state")
	}
	state2 := r.Get("key1", "id1")
	if state1 != state2 {
		t.Error("expected same state for same key")
	}
	state3 := r.Get("key2", "id1")
	if state1 == state3 {
		t.Error("expected different state for different key")
	}
}

func TestToolLoopRegistryGetNil(t *testing.T) {
	var r *ToolLoopRegistry
	state := r.Get("key", "id")
	if state != nil {
		t.Error("expected nil state from nil registry")
	}
}

// ---------------------------------------------------------------------------
// RecordToolCall
// ---------------------------------------------------------------------------

func TestRecordToolCall(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	RecordToolCall(state, "exec", map[string]any{"command": "ls"}, "call_1", config)
	if len(state.History) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(state.History))
	}
	if state.History[0].ToolName != "exec" {
		t.Errorf("expected toolName=exec, got %q", state.History[0].ToolName)
	}
	if state.History[0].ToolCallID != "call_1" {
		t.Errorf("expected toolCallID=call_1, got %q", state.History[0].ToolCallID)
	}
}

func TestRecordToolCallNilState(t *testing.T) {
	// Should not panic.
	RecordToolCall(nil, "exec", map[string]any{}, "id", enabledConfig())
}

func TestRecordToolCallTrimsHistory(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := core.ToolLoopDetectionConfig{
		Enabled:     boolPtr(true),
		HistorySize: 5,
	}
	for i := 0; i < 10; i++ {
		RecordToolCall(state, "exec", map[string]any{"i": i}, fmt.Sprintf("call_%d", i), config)
	}
	if len(state.History) > 5 {
		t.Errorf("expected at most 5 entries, got %d", len(state.History))
	}
}

// ---------------------------------------------------------------------------
// RecordToolCallOutcome
// ---------------------------------------------------------------------------

func TestRecordToolCallOutcome(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	RecordToolCall(state, "exec", map[string]any{"command": "ls"}, "call_1", config)
	RecordToolCallOutcome(state, "exec", map[string]any{"command": "ls"}, "call_1",
		core.ToolResult{Text: "file1.txt"}, nil, config)

	if len(state.History) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(state.History))
	}
	if state.History[0].ResultHash == "" {
		t.Error("expected non-empty result hash")
	}
}

func TestRecordToolCallOutcomeWithError(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	RecordToolCall(state, "exec", map[string]any{"command": "fail"}, "call_2", config)
	RecordToolCallOutcome(state, "exec", map[string]any{"command": "fail"}, "call_2",
		core.ToolResult{}, fmt.Errorf("permission denied"), config)

	if state.History[0].ResultHash == "" {
		t.Error("expected non-empty result hash for error")
	}
}

func TestRecordToolCallOutcomeNilState(t *testing.T) {
	// Should not panic.
	RecordToolCallOutcome(nil, "exec", map[string]any{}, "id", core.ToolResult{}, nil, enabledConfig())
}

// ---------------------------------------------------------------------------
// DetectToolCallLoop — disabled
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopDisabled(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := core.ToolLoopDetectionConfig{Enabled: boolPtr(false)}
	result := DetectToolCallLoop(state, "exec", map[string]any{}, config)
	if result.Stuck {
		t.Error("expected not stuck when disabled")
	}
}

func TestDetectToolCallLoopNilState(t *testing.T) {
	config := enabledConfig()
	result := DetectToolCallLoop(nil, "exec", map[string]any{}, config)
	if result.Stuck {
		t.Error("expected not stuck for nil state")
	}
}

// ---------------------------------------------------------------------------
// DetectToolCallLoop — generic repeat
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopGenericRepeat(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	params := map[string]any{"command": "ls"}

	// Record enough identical calls to trigger warning.
	for i := 0; i < 3; i++ {
		RecordToolCall(state, "exec", params, fmt.Sprintf("call_%d", i), config)
	}

	result := DetectToolCallLoop(state, "exec", params, config)
	if !result.Stuck {
		t.Error("expected stuck after repeated calls")
	}
	if result.Detector != ToolLoopDetectorGenericRepeat {
		t.Errorf("expected generic_repeat detector, got %v", result.Detector)
	}
	if result.Level != "warning" {
		t.Errorf("expected warning level, got %q", result.Level)
	}
}

// ---------------------------------------------------------------------------
// DetectToolCallLoop — known poll no progress
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopKnownPollNoProgress(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	params := map[string]any{"action": "poll", "id": "proc_1"}

	for i := 0; i < 3; i++ {
		RecordToolCall(state, "process", params, fmt.Sprintf("call_%d", i), config)
		RecordToolCallOutcome(state, "process", params, fmt.Sprintf("call_%d", i),
			core.ToolResult{Text: "still running"}, nil, config)
	}

	result := DetectToolCallLoop(state, "process", params, config)
	if !result.Stuck {
		t.Error("expected stuck for poll no progress")
	}
	if result.Detector != ToolLoopDetectorKnownPollNoProgress {
		t.Errorf("expected known_poll_no_progress detector, got %v", result.Detector)
	}
}

// ---------------------------------------------------------------------------
// DetectToolCallLoop — global circuit breaker
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopGlobalCircuitBreaker(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()
	params := map[string]any{"command": "check"}

	for i := 0; i < 8; i++ {
		RecordToolCall(state, "exec", params, fmt.Sprintf("call_%d", i), config)
		RecordToolCallOutcome(state, "exec", params, fmt.Sprintf("call_%d", i),
			core.ToolResult{Text: "same result"}, nil, config)
	}

	result := DetectToolCallLoop(state, "exec", params, config)
	if !result.Stuck {
		t.Error("expected stuck for circuit breaker")
	}
	if result.Detector != ToolLoopDetectorGlobalCircuitBreaker {
		t.Errorf("expected global_circuit_breaker, got %v", result.Detector)
	}
	if result.Level != "critical" {
		t.Errorf("expected critical level, got %q", result.Level)
	}
}

// ---------------------------------------------------------------------------
// DetectToolCallLoop — no loop
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopNoLoop(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := enabledConfig()

	// Different args each time.
	for i := 0; i < 5; i++ {
		params := map[string]any{"command": fmt.Sprintf("cmd_%d", i)}
		RecordToolCall(state, "exec", params, fmt.Sprintf("call_%d", i), config)
	}

	result := DetectToolCallLoop(state, "exec", map[string]any{"command": "cmd_new"}, config)
	if result.Stuck {
		t.Error("expected not stuck with different args")
	}
}

// ---------------------------------------------------------------------------
// ShouldEmitToolLoopWarning
// ---------------------------------------------------------------------------

func TestShouldEmitToolLoopWarning(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}

	// First warning at bucket 1 (count 10-19) should emit.
	if !ShouldEmitToolLoopWarning(state, "key1", 10) {
		t.Error("expected true for first warning in bucket")
	}
	// Same bucket should not re-emit.
	if ShouldEmitToolLoopWarning(state, "key1", 15) {
		t.Error("expected false for same bucket")
	}
	// Next bucket should emit.
	if !ShouldEmitToolLoopWarning(state, "key1", 20) {
		t.Error("expected true for next bucket")
	}
}

func TestShouldEmitToolLoopWarningNilState(t *testing.T) {
	if ShouldEmitToolLoopWarning(nil, "key", 10) {
		t.Error("expected false for nil state")
	}
}

func TestShouldEmitToolLoopWarningEmptyKey(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	if ShouldEmitToolLoopWarning(state, "", 10) {
		t.Error("expected false for empty key")
	}
}

// ---------------------------------------------------------------------------
// Ping-pong detection
// ---------------------------------------------------------------------------

func TestDetectToolCallLoopPingPong(t *testing.T) {
	state := &ToolLoopSessionState{WarningBuckets: map[string]int{}}
	config := core.ToolLoopDetectionConfig{
		Enabled:                       boolPtr(true),
		WarningThreshold:              3,
		CriticalThreshold:             5,
		GlobalCircuitBreakerThreshold: 10,
	}
	paramsA := map[string]any{"command": "check_a"}
	paramsB := map[string]any{"command": "check_b"}

	// Alternate A-B-A-B with identical results.
	for i := 0; i < 4; i++ {
		if i%2 == 0 {
			RecordToolCall(state, "exec", paramsA, fmt.Sprintf("call_%d", i), config)
			RecordToolCallOutcome(state, "exec", paramsA, fmt.Sprintf("call_%d", i),
				core.ToolResult{Text: "result_a"}, nil, config)
		} else {
			RecordToolCall(state, "exec", paramsB, fmt.Sprintf("call_%d", i), config)
			RecordToolCallOutcome(state, "exec", paramsB, fmt.Sprintf("call_%d", i),
				core.ToolResult{Text: "result_b"}, nil, config)
		}
	}

	result := DetectToolCallLoop(state, "exec", paramsA, config)
	if !result.Stuck {
		t.Error("expected stuck for ping-pong pattern")
	}
	if result.Detector != ToolLoopDetectorPingPong {
		t.Errorf("expected ping_pong detector, got %v", result.Detector)
	}
}
