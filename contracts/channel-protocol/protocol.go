// Package protocol defines the message types exchanged between
// channel-server and adapters (slack-adapter, etc.) over Unix socket.
//
// This package is source-independent: it contains no Slack, Discord,
// or other platform-specific logic.
package protocol

import "encoding/json"

// MessageType identifies the message type in the framing protocol.
type MessageType string

const (
	MsgRegister     MessageType = "register"
	MsgMessage      MessageType = "message"
	MsgReply        MessageType = "reply"
	MsgReplyAck     MessageType = "reply_ack"
	MsgPermission   MessageType = "permission"
	MsgDisconnected MessageType = "disconnected"
)

// Envelope wraps all messages on the Unix socket.
// Wire format: 4-byte big-endian length prefix + JSON payload.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// UnmarshalPayload unmarshals the envelope payload into the given target.
func (e *Envelope) UnmarshalPayload(target any) error {
	return json.Unmarshal(e.Payload, target)
}

// NewEnvelope creates a serialized envelope from a type and payload.
func NewEnvelope(t MessageType, payload any) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: t, Payload: p})
}

// RegisterPayload is sent by the adapter on connect to identify the session.
type RegisterPayload struct {
	ThreadTS  string `json:"thread_ts"`
	ChannelID string `json:"channel_id"`
}

// MessagePayload carries a message from an external source to channel-server.
//
// Source names the adapter-side provenance of the message. An adapter sets it
// (e.g. "slack") only on messages that came from an authenticated interactive
// user; system/content deliveries (notifications, relayed events) leave it
// empty. channel-server requires a non-empty Source before it will treat a
// "y <id>" / "n <id>" message as a permission verdict, so a forged content
// event cannot impersonate a human approval.
type MessagePayload struct {
	User     string `json:"user"`
	UserID   string `json:"user_id"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts"`
	Source   string `json:"source,omitempty"`
}

// ReplyPayload carries a reply from channel-server back to the adapter.
type ReplyPayload struct {
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts"`
}

// ReplyAckPayload is sent by the adapter after posting the reply.
type ReplyAckPayload struct {
	Timestamp string `json:"ts,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PermissionPayload carries a permission prompt from channel-server to adapter.
type PermissionPayload struct {
	ThreadTS string `json:"thread_ts"`
	Text     string `json:"text"`
}
