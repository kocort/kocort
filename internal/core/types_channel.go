package core

// ---------------------------------------------------------------------------
// Value Objects — Channel Messages
// ---------------------------------------------------------------------------

type ChannelInboundMessage struct {
	Channel     string
	AccountID   string
	From        string
	To          string
	ThreadID    string
	ChatType    ChatType
	Text        string
	Attachments []Attachment
	AgentID     string
	MessageID   string
	Raw         map[string]any
}

type ChannelOutboundMessage struct {
	Channel   string
	AccountID string
	To        string
	AllowFrom []string
	Mode      string
	ThreadID  string
	ReplyToID string
	Payload   ReplyPayload
}

type ChannelDeliveryResult struct {
	Channel        string
	MessageID      string
	ChatID         string
	ChannelID      string
	RoomID         string
	ConversationID string
	Timestamp      int64
	ToJID          string
	PollID         string
	Meta           map[string]any
}
