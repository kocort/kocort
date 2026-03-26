package session

import (
	"testing"
)

func TestEvaluateSendPolicy_AllowByDefault(t *testing.T) {
	policy := DefaultSendPolicy()
	result := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:    "slack",
		ChatType:   "direct",
		SessionKey: "sess-abc",
	})
	if !result.Allowed {
		t.Fatalf("expected allowed, got denied: %s", result.Reason)
	}
	if result.Rule != nil {
		t.Fatalf("expected no matching rule, got %+v", result.Rule)
	}
}

func TestEvaluateSendPolicy_DenyByRule(t *testing.T) {
	policy := SendPolicyConfig{
		DefaultAction: SendPolicyAllow,
		Rules: []SendPolicyRule{
			{Action: SendPolicyDeny, Channel: "discord", Reason: "discord blocked"},
		},
	}
	result := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:    "discord",
		ChatType:   "group",
		SessionKey: "sess-xyz",
	})
	if result.Allowed {
		t.Fatalf("expected denied, got allowed")
	}
	if result.Rule == nil || result.Rule.Reason != "discord blocked" {
		t.Fatalf("expected matching deny rule, got %+v", result.Rule)
	}
}

func TestEvaluateSendPolicy_AllowBySpecificRule(t *testing.T) {
	policy := SendPolicyConfig{
		DefaultAction: SendPolicyDeny,
		Rules: []SendPolicyRule{
			{Action: SendPolicyAllow, Channel: "slack", ChatType: "direct", Reason: "slack direct allowed"},
		},
	}
	// Matching rule
	result := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:  "slack",
		ChatType: "direct",
	})
	if !result.Allowed {
		t.Fatalf("expected allowed by rule, got denied")
	}

	// Not matching — falls through to default deny
	result2 := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:  "slack",
		ChatType: "group",
	})
	if result2.Allowed {
		t.Fatalf("expected denied by default, got allowed")
	}
}

func TestEvaluateSendPolicy_KeyPrefixMatch(t *testing.T) {
	policy := SendPolicyConfig{
		DefaultAction: SendPolicyAllow,
		Rules: []SendPolicyRule{
			{Action: SendPolicyDeny, KeyPrefix: "admin-", Reason: "admin sessions blocked"},
		},
	}
	result := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:    "slack",
		SessionKey: "admin-session-42",
	})
	if result.Allowed {
		t.Fatalf("expected denied for admin prefix, got allowed")
	}

	result2 := EvaluateSendPolicy(policy, SendPolicyInput{
		Channel:    "slack",
		SessionKey: "user-session-1",
	})
	if !result2.Allowed {
		t.Fatalf("expected allowed for user prefix, got denied")
	}
}

func TestMergeSendPolicies(t *testing.T) {
	p1 := SendPolicyConfig{
		DefaultAction: SendPolicyAllow,
		Rules: []SendPolicyRule{
			{Action: SendPolicyDeny, Channel: "discord"},
		},
	}
	p2 := SendPolicyConfig{
		DefaultAction: SendPolicyDeny,
		Rules: []SendPolicyRule{
			{Action: SendPolicyAllow, Channel: "slack"},
		},
	}
	merged := MergeSendPolicies(p1, p2)

	if merged.DefaultAction != SendPolicyDeny {
		t.Fatalf("expected last defaultAction=deny, got %s", merged.DefaultAction)
	}
	if len(merged.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(merged.Rules))
	}
}
