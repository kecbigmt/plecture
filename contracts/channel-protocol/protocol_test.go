package protocol

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelopeUnmarshalPayloadRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		msgType MessageType
		payload any
	}{
		{"register", MsgRegister, RegisterPayload{ThreadTS: "1.1", ChannelID: "C1"}},
		{"message", MsgMessage, MessagePayload{User: "alice", UserID: "U1", Text: "hi", ThreadTS: "1.1", Source: "chat"}},
		{"message with empty source", MsgMessage, MessagePayload{User: "bot", UserID: "U2", Text: "hi", ThreadTS: "1.1"}},
		{"reply", MsgReply, ReplyPayload{Text: "reply text", ThreadTS: "1.1"}},
		{"reply_ack success", MsgReplyAck, ReplyAckPayload{Timestamp: "1.2"}},
		{"reply_ack error", MsgReplyAck, ReplyAckPayload{Error: "failed to post"}},
		{"permission", MsgPermission, PermissionPayload{ThreadTS: "1.1", Text: "allow?"}},
		{"disconnected", MsgDisconnected, struct{}{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := NewEnvelope(c.msgType, c.payload)
			if err != nil {
				t.Fatalf("NewEnvelope: %v", err)
			}

			var env Envelope
			if err := json.Unmarshal(b, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if env.Type != c.msgType {
				t.Fatalf("Type = %q, want %q", env.Type, c.msgType)
			}

			wantPayload, err := json.Marshal(c.payload)
			if err != nil {
				t.Fatalf("marshal want payload: %v", err)
			}
			var got, want map[string]any
			if len(wantPayload) > 0 && string(wantPayload) != "{}" {
				if err := json.Unmarshal(wantPayload, &want); err != nil {
					t.Fatalf("unmarshal want payload: %v", err)
				}
				if err := env.UnmarshalPayload(&got); err != nil {
					t.Fatalf("UnmarshalPayload: %v", err)
				}
				if len(got) != len(want) {
					t.Fatalf("UnmarshalPayload got %#v, want %#v", got, want)
				}
				for k, v := range want {
					if got[k] != v {
						t.Errorf("field %q = %#v, want %#v", k, got[k], v)
					}
				}
			}
		})
	}
}

func TestUnmarshalPayloadIntoTypedTarget(t *testing.T) {
	b, err := NewEnvelope(MsgRegister, RegisterPayload{ThreadTS: "9.9", ChannelID: "C9"})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var got RegisterPayload
	if err := env.UnmarshalPayload(&got); err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	want := RegisterPayload{ThreadTS: "9.9", ChannelID: "C9"}
	if got != want {
		t.Fatalf("UnmarshalPayload = %+v, want %+v", got, want)
	}
}

func TestUnmarshalPayloadRejectsMismatchedTarget(t *testing.T) {
	env := Envelope{Type: MsgRegister, Payload: json.RawMessage(`{"thread_ts": 123}`)}
	var got RegisterPayload
	if err := env.UnmarshalPayload(&got); err == nil {
		t.Fatalf("expected error unmarshaling numeric field into string target")
	}
}

func TestNewEnvelopeRejectsUnmarshalablePayload(t *testing.T) {
	if _, err := NewEnvelope(MsgMessage, make(chan int)); err == nil {
		t.Fatalf("expected error marshaling an unmarshalable payload")
	}
}

func TestMessagePayloadSourceOmitsWhenEmpty(t *testing.T) {
	b, err := json.Marshal(MessagePayload{User: "u", UserID: "id", Text: "t", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["source"]; ok {
		t.Fatalf("expected \"source\" to be omitted when empty, got %#v", raw)
	}
}
