package adapter

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/plugins/session/runtime/src/channel-server/server"
)

// TestSocketClient_SendAndReceive tests the full round-trip:
// slack-adapter (SocketClient) -> channel-server (SocketListener) -> reply back.
func TestSocketClient_SendAndReceive(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	// Track messages received by channel-server
	var serverReceived []protocol.Envelope
	var mu sync.Mutex

	// Start channel-server's socket listener
	listener, err := server.NewSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		mu.Lock()
		serverReceived = append(serverReceived, env)
		mu.Unlock()

		// If it's a regular message, send a reply back
		if env.Type == protocol.MsgMessage {
			reply := protocol.ReplyPayload{
				Text:     "```\nGot your message\n```",
				ThreadTS: "1234567890.123456",
			}
			data, _ := protocol.NewEnvelope(protocol.MsgReply, reply)
			server.WriteMessageTo(conn, data)
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	// Track replies received by slack-adapter
	var replies []protocol.ReplyPayload
	var replyMu sync.Mutex

	// Create slack-adapter's socket client
	client, err := NewSocketClient(socketPath, "1234567890.123456", "C01TEST", testLogger(),
		func(reply protocol.ReplyPayload) {
			replyMu.Lock()
			replies = append(replies, reply)
			replyMu.Unlock()
		},
		nil, // no permission handler needed for this test
	)
	if err != nil {
		t.Fatalf("NewSocketClient() error: %v", err)
	}
	defer client.Close()

	go client.ReadLoop()

	// Wait for registration to be processed
	time.Sleep(50 * time.Millisecond)

	// Send a message from slack-adapter to channel-server
	err = client.SendMessage(protocol.MessagePayload{
		User:     "testuser",
		UserID:   "U123",
		Text:     "hello from slack",
		ThreadTS: "1234567890.123456",
	})
	if err != nil {
		t.Fatalf("SendMessage() error: %v", err)
	}

	// Wait for round-trip
	deadline := time.After(2 * time.Second)
	for {
		replyMu.Lock()
		n := len(replies)
		replyMu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for reply")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Verify channel-server received 2 envelopes (register + message)
	mu.Lock()
	if len(serverReceived) != 2 {
		t.Errorf("server received %d messages, want 2 (register + message)", len(serverReceived))
	}
	// First should be register
	if serverReceived[0].Type != protocol.MsgRegister {
		t.Errorf("first message type = %q, want %q", serverReceived[0].Type, protocol.MsgRegister)
	}
	// Second should be slack_message
	if serverReceived[1].Type != protocol.MsgMessage {
		t.Errorf("second message type = %q, want %q", serverReceived[1].Type, protocol.MsgMessage)
	}
	mu.Unlock()

	// Verify reply
	replyMu.Lock()
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(replies))
	}
	if replies[0].Text != "```\nGot your message\n```" {
		t.Errorf("reply text = %q", replies[0].Text)
	}
	replyMu.Unlock()
}

func TestSocketClient_PermissionFlow(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	// Channel-server: on message, send a permission prompt back
	listener, err := server.NewSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			// Parse the message to check if it's a permission reply
			var msg protocol.MessagePayload
			json.Unmarshal(env.Payload, &msg)

			if msg.Text == "trigger_permission" {
				perm := protocol.PermissionPayload{
					ThreadTS: "1234567890.123456",
					Text:     "*Permission request*\nClaude wants to run `Bash`: ls\n\nReply `y abcde` to allow.",
				}
				data, _ := protocol.NewEnvelope(protocol.MsgPermission, perm)
				server.WriteMessageTo(conn, data)
			}
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	var perms []protocol.PermissionPayload
	var mu sync.Mutex

	client, err := NewSocketClient(socketPath, "1234567890.123456", "C01TEST", testLogger(),
		nil,
		func(perm protocol.PermissionPayload) {
			mu.Lock()
			perms = append(perms, perm)
			mu.Unlock()
		},
	)
	if err != nil {
		t.Fatalf("NewSocketClient() error: %v", err)
	}
	defer client.Close()

	go client.ReadLoop()
	time.Sleep(50 * time.Millisecond)

	// Send a message that triggers a permission prompt
	client.SendMessage(protocol.MessagePayload{
		User: "test", Text: "trigger_permission", ThreadTS: "1234567890.123456",
	})

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(perms)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for permission prompt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	if len(perms) != 1 {
		t.Fatalf("got %d permission prompts, want 1", len(perms))
	}
	if perms[0].Text == "" {
		t.Error("permission text should not be empty")
	}
	mu.Unlock()
}
