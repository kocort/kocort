package session

import "strings"

type ThreadBindingTargetKind string

const (
	ThreadBindingTargetKindSubagent ThreadBindingTargetKind = "subagent"
	ThreadBindingTargetKindSession  ThreadBindingTargetKind = "session"
)

type ThreadBindingConversationRef struct {
	Channel              string `json:"channel"`
	AccountID            string `json:"accountId"`
	ConversationID       string `json:"conversationId"`
	ParentConversationID string `json:"parentConversationId,omitempty"`
}

type ThreadBindingAdapterCapabilities struct {
	Placements      []ThreadBindingPlacement
	BindSupported   bool
	UnbindSupported bool
}

type ThreadBindingAdapter interface {
	Channel() string
	AccountID() string
	Capabilities() ThreadBindingAdapterCapabilities
	Bind(input BindThreadSessionInput) (SessionBindingRecord, error)
	ListBySession(targetSessionKey string) []SessionBindingRecord
	ResolveByConversation(ref ThreadBindingConversationRef) (SessionBindingRecord, bool)
	Touch(bindingID string) bool
	Unbind(targetSessionKey string, reason string) ([]SessionBindingRecord, error)
}

func normalizeThreadBindingAdapterKey(channel string, accountID string) string {
	return strings.ToLower(strings.TrimSpace(channel)) + ":" + strings.TrimSpace(accountID)
}

