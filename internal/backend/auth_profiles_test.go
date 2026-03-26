package backend

import (
	"fmt"
	"testing"
	"time"
)

// ---------- Cooldown calculation tests ----------

func TestComputeStandardCooldownDuration(t *testing.T) {
	tests := []struct {
		errorCount int
		min        time.Duration
		max        time.Duration
	}{
		{0, 0, 0},
		{1, 59 * time.Second, 61 * time.Second},          // 60s * 5^0 = 60s
		{2, 4*time.Minute + 50*time.Second, 5*time.Minute + 10*time.Second}, // 60s * 5^1 = 300s = 5m
		{3, 24 * time.Minute, 26 * time.Minute},          // 60s * 5^2 = 1500s = 25m
		{4, 59 * time.Minute, 61 * time.Minute},          // 60s * 5^3 = 7500s → capped at 1h
		{10, 59 * time.Minute, 61 * time.Minute},         // Still capped at 1h
	}
	for _, tc := range tests {
		d := ComputeStandardCooldownDuration(tc.errorCount)
		if d < tc.min || d > tc.max {
			t.Errorf("errorCount=%d: got %v, want [%v, %v]", tc.errorCount, d, tc.min, tc.max)
		}
	}
}

func TestComputeBillingDisableDuration(t *testing.T) {
	tests := []struct {
		errorCount int
		min        time.Duration
		max        time.Duration
	}{
		{0, 0, 0},
		{1, 4*time.Hour + 59*time.Minute, 5*time.Hour + 1*time.Minute},       // 5h * 2^0 = 5h
		{2, 9*time.Hour + 59*time.Minute, 10*time.Hour + 1*time.Minute},      // 5h * 2^1 = 10h
		{3, 19*time.Hour + 59*time.Minute, 20*time.Hour + 1*time.Minute},     // 5h * 2^2 = 20h
		{4, 23*time.Hour + 59*time.Minute, 24*time.Hour + 1*time.Minute},     // 5h * 2^3 = 40h → capped at 24h
	}
	for _, tc := range tests {
		d := ComputeBillingDisableDuration(tc.errorCount)
		if d < tc.min || d > tc.max {
			t.Errorf("errorCount=%d: got %v, want [%v, %v]", tc.errorCount, d, tc.min, tc.max)
		}
	}
}

func TestComputeAuthCooldownDuration(t *testing.T) {
	tests := []struct {
		errorCount int
		min        time.Duration
		max        time.Duration
	}{
		{0, 0, 0},
		{1, time.Minute + 59*time.Second, 2*time.Minute + 1*time.Second},     // 120s * 5^0 = 2m
		{2, 9*time.Minute + 50*time.Second, 10*time.Minute + 10*time.Second}, // 120s * 5^1 = 10m
		{3, 49 * time.Minute, 51 * time.Minute},                              // 120s * 5^2 = 50m
		{4, time.Hour + 59*time.Minute, 2*time.Hour + 1*time.Minute},         // 120s * 5^3 = 250m → capped at 2h
	}
	for _, tc := range tests {
		d := ComputeAuthCooldownDuration(tc.errorCount)
		if d < tc.min || d > tc.max {
			t.Errorf("errorCount=%d: got %v, want [%v, %v]", tc.errorCount, d, tc.min, tc.max)
		}
	}
}

func TestIsProbeEligible(t *testing.T) {
	now := time.Now()

	// Not in cooldown → not eligible
	if IsProbeEligible(now.Add(-1*time.Minute), time.Time{}) {
		t.Error("should not be eligible when not in cooldown")
	}

	// In cooldown but too far from expiry → not eligible
	if IsProbeEligible(now.Add(10*time.Minute), time.Time{}) {
		t.Error("should not be eligible when too far from expiry")
	}

	// In cooldown, within probe window, no recent probe → eligible
	if !IsProbeEligible(now.Add(90*time.Second), time.Time{}) {
		t.Error("should be eligible when within probe window")
	}

	// In cooldown, within probe window, but probed recently → not eligible
	if IsProbeEligible(now.Add(90*time.Second), now.Add(-10*time.Second)) {
		t.Error("should not be eligible when probed too recently")
	}
}

// ---------- AuthProfileStore tests ----------

func TestAuthProfileStore_RegisterAndSelect(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})
	store.Register(AuthProfile{ID: "key-2", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-2", ConfigIndex: 1})
	store.Register(AuthProfile{ID: "key-3", Provider: "anthropic", Type: AuthProfileTypeAPIKey, APIKey: "ak-1", ConfigIndex: 0})

	if store.CountForProvider("openai") != 2 {
		t.Fatalf("expected 2 openai profiles, got %d", store.CountForProvider("openai"))
	}
	if store.CountForProvider("anthropic") != 1 {
		t.Fatalf("expected 1 anthropic profile, got %d", store.CountForProvider("anthropic"))
	}

	// First select should return either key (both have zero LastUsed).
	p, isProbe := store.SelectProfile("openai", false)
	if p == nil {
		t.Fatal("expected a profile")
	}
	if isProbe {
		t.Fatal("should not be a probe")
	}

	// Select from non-existent provider.
	p, _ = store.SelectProfile("nonexistent", false)
	if p != nil {
		t.Fatal("expected nil for nonexistent provider")
	}
}

func TestAuthProfileStore_RoundRobin(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})
	store.Register(AuthProfile{ID: "key-2", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-2", ConfigIndex: 1})

	// Select key-1 (both have zero LastUsed, key-1 wins by iteration order).
	p1, _ := store.SelectProfile("openai", false)
	store.RecordSuccess(p1.ID)

	// Now key-2 should be selected (older LastUsed = zero).
	p2, _ := store.SelectProfile("openai", false)
	if p2.ID == p1.ID {
		t.Fatalf("expected round-robin to select different key, got %s twice", p1.ID)
	}

	store.RecordSuccess(p2.ID)

	// Now key-1 should be selected again (older LastUsed).
	p3, _ := store.SelectProfile("openai", false)
	if p3.ID != p1.ID {
		t.Fatalf("expected round-robin back to %s, got %s", p1.ID, p3.ID)
	}
}

func TestAuthProfileStore_CooldownSkips(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})
	store.Register(AuthProfile{ID: "key-2", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-2", ConfigIndex: 1})

	// Put key-1 in cooldown.
	store.RecordFailure("key-1", BackendFailureRateLimit)

	// Should select key-2 since key-1 is in cooldown.
	p, _ := store.SelectProfile("openai", false)
	if p == nil || p.ID != "key-2" {
		t.Fatalf("expected key-2 (key-1 in cooldown), got %v", p)
	}
}

func TestAuthProfileStore_BillingDisable(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})

	store.RecordFailure("key-1", BackendFailureBilling)
	st, ok := store.GetState("key-1")
	if !ok {
		t.Fatal("state not found")
	}
	if st.BillingDisabledUntil.IsZero() {
		t.Fatal("expected BillingDisabledUntil to be set")
	}
	if time.Until(st.BillingDisabledUntil) < 4*time.Hour {
		t.Fatalf("expected at least 4h billing disable, got %v", time.Until(st.BillingDisabledUntil))
	}

	// Should not be selectable.
	p, _ := store.SelectProfile("openai", false)
	if p != nil {
		t.Fatal("expected nil when all profiles billing-disabled")
	}
}

func TestAuthProfileStore_SuccessResetsCooldown(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})

	store.RecordFailure("key-1", BackendFailureRateLimit)
	store.RecordFailure("key-1", BackendFailureRateLimit)

	st, _ := store.GetState("key-1")
	if st.ConsecutiveErrors != 2 {
		t.Fatalf("expected 2 consecutive errors, got %d", st.ConsecutiveErrors)
	}

	store.RecordSuccess("key-1")
	st, _ = store.GetState("key-1")
	if st.ConsecutiveErrors != 0 {
		t.Fatalf("expected 0 consecutive errors after success, got %d", st.ConsecutiveErrors)
	}
	if !st.CooldownUntil.IsZero() {
		t.Fatal("expected cooldown cleared after success")
	}
}

func TestAuthProfileStore_ProbeWhenAllInCooldown(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1", ConfigIndex: 0})

	// Manually set a cooldown that expires in 90 seconds (within probe window).
	store.mu.Lock()
	store.states["key-1"].CooldownUntil = time.Now().Add(90 * time.Second)
	store.states["key-1"].ConsecutiveErrors = 1
	store.mu.Unlock()

	// Without probe allowed → nil.
	p, _ := store.SelectProfile("openai", false)
	if p != nil {
		t.Fatal("expected nil without probe allowed")
	}

	// With probe allowed → should return.
	p, isProbe := store.SelectProfile("openai", true)
	if p == nil {
		t.Fatal("expected probe profile")
	}
	if !isProbe {
		t.Fatal("expected isProbe=true")
	}

	// Record probe and try again → should throttle.
	store.RecordProbe("key-1")
	p, _ = store.SelectProfile("openai", true)
	if p != nil {
		t.Fatal("expected nil after recent probe (throttled)")
	}
}

// ---------- ErrorReason classification tests ----------

func TestErrorReason_NewTypes(t *testing.T) {
	tests := []struct {
		msg    string
		expect BackendFailureReason
	}{
		{"401 Unauthorized", BackendFailureAuth},
		{"403 Forbidden", BackendFailureAuth},
		{"invalid api key provided", BackendFailureAuth},
		{"invalid_api_key", BackendFailureAuth},
		{"authentication failed", BackendFailureAuth},
		{"billing issue: insufficient funds", BackendFailureBilling},
		{"quota exceeded for this month", BackendFailureBilling},
		{"insufficient_quota", BackendFailureBilling},
		{"402 Payment Required", BackendFailureBilling},
		{"429 Too Many Requests", BackendFailureRateLimit},
		{"rate limit exceeded", BackendFailureRateLimit},
		{"rate_limit_error", BackendFailureRateLimit},
		{"529 Overloaded", BackendFailureOverloaded},
		{"server overloaded, try again later", BackendFailureOverloaded},
	}
	for _, tc := range tests {
		reason := ErrorReason(fmt.Errorf("%s", tc.msg))
		if reason != tc.expect {
			t.Errorf("ErrorReason(%q) = %q, want %q", tc.msg, reason, tc.expect)
		}
	}
}

func TestAuthProfileStore_GetProfile(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-test"})

	p, ok := store.GetProfile("key-1")
	if !ok || p.APIKey != "sk-test" {
		t.Fatalf("GetProfile failed: ok=%v, profile=%+v", ok, p)
	}

	_, ok = store.GetProfile("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent profile")
	}
}

func TestAuthProfileStore_DuplicateRegister(t *testing.T) {
	store := NewAuthProfileStore()
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1"})
	store.Register(AuthProfile{ID: "key-1", Provider: "openai", Type: AuthProfileTypeAPIKey, APIKey: "sk-1-updated"})

	// Should not duplicate in provider index.
	if store.CountForProvider("openai") != 1 {
		t.Fatalf("expected 1, got %d", store.CountForProvider("openai"))
	}

	p, _ := store.GetProfile("key-1")
	if p.APIKey != "sk-1-updated" {
		t.Fatalf("expected updated key, got %s", p.APIKey)
	}
}
