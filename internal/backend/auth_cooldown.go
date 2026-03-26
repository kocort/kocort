package backend

import (
	"math"
	"time"
)

// ---------------------------------------------------------------------------
// Auth Cooldown — Exponential backoff calculations for API errors
//
// Standard cooldown:  min(1h, 60s * 5^min(errorCount-1, 3))
//   errorCount=1 → 60s,  =2 → 5m,  =3 → 25m,  ≥4 → 1h (capped)
//
// Billing disable:    min(24h, 5h * 2^min(errorCount-1, 10))
//   errorCount=1 → 5h,  =2 → 10h,  ≥3 → 20h,  ≥4 → 24h (capped)
//
// Auth error:         min(2h, 120s * 5^min(errorCount-1, 3))
//   errorCount=1 → 2m,  =2 → 10m,  =3 → 50m,  ≥4 → 2h (capped)
//
// Probe throttle:     30s per key, start probing 2 min before cooldown ends
//

// ---------------------------------------------------------------------------

const (
	// StandardCooldownBase is the base duration for standard errors (60s).
	StandardCooldownBase = 60 * time.Second
	// StandardCooldownMultiplier is the exponential multiplier (5x per error).
	StandardCooldownMultiplier = 5.0
	// StandardCooldownMaxExponent caps the exponent to prevent runaway.
	StandardCooldownMaxExponent = 3
	// StandardCooldownCap caps the maximum cooldown duration.
	StandardCooldownCap = 1 * time.Hour

	// BillingDisableBase is the base duration for billing errors (5h).
	BillingDisableBase = 5 * time.Hour
	// BillingDisableMultiplier doubles per error.
	BillingDisableMultiplier = 2.0
	// BillingDisableMaxExponent caps the exponent.
	BillingDisableMaxExponent = 10
	// BillingDisableCap caps the maximum billing disable duration.
	BillingDisableCap = 24 * time.Hour

	// AuthCooldownBase is the base duration for auth-specific errors (2m).
	AuthCooldownBase = 120 * time.Second
	// AuthCooldownMultiplier is the exponential multiplier for auth errors.
	AuthCooldownMultiplier = 5.0
	// AuthCooldownMaxExponent caps the exponent for auth errors.
	AuthCooldownMaxExponent = 3
	// AuthCooldownCap caps the maximum auth cooldown duration.
	AuthCooldownCap = 2 * time.Hour

	// ProbeThrottle is the minimum interval between probe attempts per key.
	ProbeThrottle = 30 * time.Second
	// ProbeWindow is how far before cooldown expiry we start probing.
	ProbeWindow = 2 * time.Minute
)

// ComputeStandardCooldownDuration returns the cooldown for standard transient errors.
// Formula: min(1h, 60s * 5^min(errorCount-1, 3))
func ComputeStandardCooldownDuration(errorCount int) time.Duration {
	return computeExponentialCooldown(
		errorCount,
		StandardCooldownBase,
		StandardCooldownMultiplier,
		StandardCooldownMaxExponent,
		StandardCooldownCap,
	)
}

// ComputeBillingDisableDuration returns the cooldown for billing/quota errors.
// Formula: min(24h, 5h * 2^min(errorCount-1, 10))
func ComputeBillingDisableDuration(errorCount int) time.Duration {
	return computeExponentialCooldown(
		errorCount,
		BillingDisableBase,
		BillingDisableMultiplier,
		BillingDisableMaxExponent,
		BillingDisableCap,
	)
}

// ComputeAuthCooldownDuration returns the cooldown for authentication errors.
// Formula: min(2h, 120s * 5^min(errorCount-1, 3))
func ComputeAuthCooldownDuration(errorCount int) time.Duration {
	return computeExponentialCooldown(
		errorCount,
		AuthCooldownBase,
		AuthCooldownMultiplier,
		AuthCooldownMaxExponent,
		AuthCooldownCap,
	)
}

// computeExponentialCooldown returns min(cap, base * multiplier^min(errorCount-1, maxExp)).
func computeExponentialCooldown(errorCount int, base time.Duration, multiplier float64, maxExp int, cap time.Duration) time.Duration {
	if errorCount <= 0 {
		return 0
	}
	exp := errorCount - 1
	if exp > maxExp {
		exp = maxExp
	}
	d := time.Duration(float64(base) * math.Pow(multiplier, float64(exp)))
	if d > cap {
		d = cap
	}
	return d
}

// IsInCooldown returns true if the given cooldownUntil is in the future.
func IsInCooldown(cooldownUntil time.Time) bool {
	return time.Now().Before(cooldownUntil)
}

// IsProbeEligible returns true if a profile is eligible for probing:
// - Cooldown expires within ProbeWindow
// - Last probe was more than ProbeThrottle ago
func IsProbeEligible(cooldownUntil, lastProbeAt time.Time) bool {
	now := time.Now()
	if now.After(cooldownUntil) {
		return false // Not in cooldown at all
	}
	remaining := cooldownUntil.Sub(now)
	if remaining > ProbeWindow {
		return false // Too far from cooldown expiry
	}
	if now.Sub(lastProbeAt) < ProbeThrottle {
		return false // Probed too recently
	}
	return true
}
