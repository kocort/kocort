package backend

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Auth Profiles — API key rotation and intelligent cooldown
//
// Manages multiple API keys/tokens per provider with automatic cooldown on
// errors, probe throttling, and round-robin selection.
//

// ---------------------------------------------------------------------------

// AuthProfileType classifies the authentication method of a profile.
type AuthProfileType string

const (
	AuthProfileTypeAPIKey AuthProfileType = "api_key"
	AuthProfileTypeToken  AuthProfileType = "token"
	AuthProfileTypeOAuth  AuthProfileType = "oauth"
)

// AuthProfile represents a single authentication credential for a provider.
type AuthProfile struct {
	// ID is a unique identifier for this profile (e.g. "openai-key-1").
	ID string `json:"id"`

	// Provider is the backend provider name (e.g. "openai", "anthropic").
	Provider string `json:"provider"`

	// Type classifies authentication method.
	Type AuthProfileType `json:"type"`

	// APIKey is the credential value.
	APIKey string `json:"apiKey"`

	// Label is an optional human-readable label.
	Label string `json:"label,omitempty"`

	// ConfigIndex is the original position in config for stable ordering.
	ConfigIndex int `json:"configIndex"`
}

// AuthProfileState tracks the runtime state of an auth profile.
type AuthProfileState struct {
	ProfileID string

	// LastUsed is when this profile was last successfully used.
	LastUsed time.Time

	// CooldownUntil is when this profile becomes available again.
	CooldownUntil time.Time

	// ConsecutiveErrors is the number of consecutive failures.
	ConsecutiveErrors int

	// LastErrorReason classifies the last error.
	LastErrorReason BackendFailureReason

	// BillingDisabledUntil is when billing errors are expected to clear.
	BillingDisabledUntil time.Time

	// LastProbeAt is when we last sent a probe request.
	LastProbeAt time.Time

	// TotalRequests tracks total successful requests.
	TotalRequests int64
}

// AuthProfileStore manages auth profiles and their cooldown state.
type AuthProfileStore struct {
	mu       sync.RWMutex
	profiles map[string]*AuthProfile      // profileID → profile
	states   map[string]*AuthProfileState // profileID → state
	byProv   map[string][]string          // provider → slice of profileIDs in config order
}

// NewAuthProfileStore creates a new empty store.
func NewAuthProfileStore() *AuthProfileStore {
	return &AuthProfileStore{
		profiles: make(map[string]*AuthProfile),
		states:   make(map[string]*AuthProfileState),
		byProv:   make(map[string][]string),
	}
}

// Register adds or replaces a profile. Thread-safe.
func (s *AuthProfileStore) Register(profile AuthProfile) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.profiles[profile.ID] = &profile
	if _, ok := s.states[profile.ID]; !ok {
		s.states[profile.ID] = &AuthProfileState{ProfileID: profile.ID}
	}
	// Rebuild provider index.
	ids := s.byProv[profile.Provider]
	found := false
	for _, id := range ids {
		if id == profile.ID {
			found = true
			break
		}
	}
	if !found {
		s.byProv[profile.Provider] = append(ids, profile.ID)
	}
}

// SelectProfile returns the best available profile for a provider.
// Returns nil if no profiles are registered or all are in cooldown.
// When probeAllowed is true, a single cooled-down profile may be returned
// if it's within the probe window (cooldown expires within 2 minutes).
func (s *AuthProfileStore) SelectProfile(provider string, probeAllowed bool) (*AuthProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.byProv[provider]
	if len(ids) == 0 {
		return nil, false
	}

	now := time.Now()
	var bestID string
	var bestLastUsed time.Time
	bestFound := false

	// Phase 1: Find best available (not in cooldown) profile.
	// Selection: round-robin via oldest LastUsed, breaking ties by config order.
	for _, id := range ids {
		st := s.states[id]
		if st == nil {
			continue
		}
		// Skip profiles in cooldown or billing-disabled.
		if now.Before(st.CooldownUntil) || now.Before(st.BillingDisabledUntil) {
			continue
		}
		if !bestFound || st.LastUsed.Before(bestLastUsed) {
			bestID = id
			bestLastUsed = st.LastUsed
			bestFound = true
		}
	}

	if bestFound {
		p := s.profiles[bestID]
		return p, false
	}

	// Phase 2: No available profiles. Try probe if allowed.
	if !probeAllowed {
		return nil, false
	}

	probeWindow := 2 * time.Minute
	probeThrottle := 30 * time.Second

	for _, id := range ids {
		st := s.states[id]
		if st == nil {
			continue
		}
		// Skip billing-disabled (longer cooldown, don't probe).
		if now.Before(st.BillingDisabledUntil) {
			continue
		}
		// Only probe if cooldown expires within the probe window.
		if st.CooldownUntil.Sub(now) > probeWindow {
			continue
		}
		// Throttle: at most one probe per 30s per profile.
		if now.Sub(st.LastProbeAt) < probeThrottle {
			continue
		}
		p := s.profiles[id]
		return p, true // isProbe=true
	}

	return nil, false
}

// RecordSuccess marks a profile as successfully used.
func (s *AuthProfileStore) RecordSuccess(profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.states[profileID]
	if st == nil {
		return
	}
	st.LastUsed = time.Now()
	st.ConsecutiveErrors = 0
	st.CooldownUntil = time.Time{}
	st.LastErrorReason = ""
	st.TotalRequests++
}

// RecordFailure marks a profile as failed and applies cooldown.
func (s *AuthProfileStore) RecordFailure(profileID string, reason BackendFailureReason) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.states[profileID]
	if st == nil {
		return
	}
	st.ConsecutiveErrors++
	st.LastErrorReason = reason
	now := time.Now()

	switch reason {
	case BackendFailureBilling:
		st.BillingDisabledUntil = now.Add(ComputeBillingDisableDuration(st.ConsecutiveErrors))
		st.CooldownUntil = st.BillingDisabledUntil
	case BackendFailureAuth:
		// Auth errors get longer cooldowns since they likely won't self-resolve.
		st.CooldownUntil = now.Add(ComputeAuthCooldownDuration(st.ConsecutiveErrors))
	default:
		st.CooldownUntil = now.Add(ComputeStandardCooldownDuration(st.ConsecutiveErrors))
	}
}

// RecordProbe marks a profile as having been probed.
func (s *AuthProfileStore) RecordProbe(profileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.states[profileID]
	if st == nil {
		return
	}
	st.LastProbeAt = time.Now()
}

// ProfilesForProvider returns all registered profile IDs for a provider.
func (s *AuthProfileStore) ProfilesForProvider(provider string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.byProv[provider]...)
}

// GetState returns a snapshot of the state for a profile.
func (s *AuthProfileStore) GetState(profileID string) (AuthProfileState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.states[profileID]
	if st == nil {
		return AuthProfileState{}, false
	}
	return *st, true
}

// GetProfile returns a profile by ID.
func (s *AuthProfileStore) GetProfile(profileID string) (*AuthProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.profiles[profileID]
	if p == nil {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// Profile count for a provider.
func (s *AuthProfileStore) CountForProvider(provider string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byProv[provider])
}
