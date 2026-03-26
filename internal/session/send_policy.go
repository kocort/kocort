package session

import (
	"strings"
)

// ---------------------------------------------------------------------------
// Send Policy — allow/deny rule matching for session message routing
// ---------------------------------------------------------------------------

// SendPolicyAction is "allow" or "deny".
type SendPolicyAction string

const (
	SendPolicyAllow SendPolicyAction = "allow"
	SendPolicyDeny  SendPolicyAction = "deny"
)

// SendPolicyRule is a single rule in a send policy chain.
type SendPolicyRule struct {
	Action    SendPolicyAction `json:"action"`
	Channel   string           `json:"channel,omitempty"`   // exact match or "*"
	ChatType  string           `json:"chatType,omitempty"`  // "direct", "group", "thread", or "*"
	KeyPrefix string           `json:"keyPrefix,omitempty"` // session key prefix match
	Reason    string           `json:"reason,omitempty"`    // human-readable reason
}

// SendPolicyConfig is the full policy configuration.
type SendPolicyConfig struct {
	DefaultAction SendPolicyAction `json:"defaultAction"` // "allow" or "deny"
	Rules         []SendPolicyRule `json:"rules,omitempty"`
}

// SendPolicyResult is the evaluation result of a send policy.
type SendPolicyResult struct {
	Allowed bool
	Action  SendPolicyAction
	Reason  string
	Rule    *SendPolicyRule // the matching rule, nil if default was used
}

// SendPolicyInput carries the context for evaluating a send policy.
type SendPolicyInput struct {
	Channel    string
	ChatType   string
	SessionKey string
}

// EvaluateSendPolicy evaluates the send policy rules against the given input.
// Rules are evaluated in order; the first matching rule wins. If no rule
// matches, the defaultAction is applied.
func EvaluateSendPolicy(policy SendPolicyConfig, input SendPolicyInput) SendPolicyResult {
	defaultAction := policy.DefaultAction
	if defaultAction == "" {
		defaultAction = SendPolicyAllow
	}

	for i := range policy.Rules {
		rule := &policy.Rules[i]
		if matchesSendPolicyRule(rule, input) {
			return SendPolicyResult{
				Allowed: rule.Action == SendPolicyAllow,
				Action:  rule.Action,
				Reason:  rule.Reason,
				Rule:    rule,
			}
		}
	}

	return SendPolicyResult{
		Allowed: defaultAction == SendPolicyAllow,
		Action:  defaultAction,
		Reason:  "default policy",
	}
}

// matchesSendPolicyRule checks if all non-empty fields of the rule match.
// An empty field is treated as a wildcard (matches anything).
func matchesSendPolicyRule(rule *SendPolicyRule, input SendPolicyInput) bool {
	// Channel match.
	if rule.Channel != "" && rule.Channel != "*" {
		if !strings.EqualFold(strings.TrimSpace(rule.Channel), strings.TrimSpace(input.Channel)) {
			return false
		}
	}

	// ChatType match.
	if rule.ChatType != "" && rule.ChatType != "*" {
		if !strings.EqualFold(strings.TrimSpace(rule.ChatType), strings.TrimSpace(input.ChatType)) {
			return false
		}
	}

	// KeyPrefix match.
	if rule.KeyPrefix != "" && rule.KeyPrefix != "*" {
		if !strings.HasPrefix(strings.TrimSpace(input.SessionKey), strings.TrimSpace(rule.KeyPrefix)) {
			return false
		}
	}

	return true
}

// DefaultSendPolicy returns a permissive default policy.
func DefaultSendPolicy() SendPolicyConfig {
	return SendPolicyConfig{
		DefaultAction: SendPolicyAllow,
	}
}

// MergeSendPolicies merges multiple policies; rules are concatenated in order.
// The last defaultAction wins.
func MergeSendPolicies(policies ...SendPolicyConfig) SendPolicyConfig {
	merged := SendPolicyConfig{DefaultAction: SendPolicyAllow}
	for _, p := range policies {
		if p.DefaultAction != "" {
			merged.DefaultAction = p.DefaultAction
		}
		merged.Rules = append(merged.Rules, p.Rules...)
	}
	return merged
}
