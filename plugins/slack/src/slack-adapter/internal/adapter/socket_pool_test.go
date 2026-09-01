package adapter

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/contracts/channel-protocol"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

type mockPoster struct {
	mu          sync.Mutex
	posts       []postedMessage
	statusCalls []postedStatus
	err         error // when set, PostToThread records the attempt then returns it
}

type postedMessage struct {
	channelID string
	threadTS  string
	text      string
}

type postedStatus struct {
	channelID, threadTS, status string
	loadingMessages             []string
}

func (m *mockPoster) PostToThread(channelID, threadTS, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.posts = append(m.posts, postedMessage{channelID, threadTS, text})
	if m.err != nil {
		return "", m.err
	}
	return "ts", nil
}

func (m *mockPoster) SetThreadStatus(channelID, threadTS, status string, loadingMessages []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusCalls = append(m.statusCalls, postedStatus{channelID, threadTS, status, loadingMessages})
	return nil
}

func (m *mockPoster) postCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.posts)
}

func (m *mockPoster) statusCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.statusCalls)
}

func TestSocketPool_SendAndReceive(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	// Track what channel-server receives
	var received []protocol.MessagePayload
	var mu sync.Mutex

	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			var msg protocol.MessagePayload
			json.Unmarshal(env.Payload, &msg)
			mu.Lock()
			received = append(received, msg)
			mu.Unlock()
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{}
	router := NewSocketPool(poster, testLogger(), nil, nil)
	defer router.Close()

	msg := protocol.MessagePayload{
		User:     "testuser",
		UserID:   "U123",
		Text:     "hello",
		ThreadTS: "1234567890.123456",
	}

	// Send should auto-connect
	err = router.Send(socketPath, "C01TEST", msg)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// Wait for message
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for message at channel-server")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	if received[0].Text != "hello" {
		t.Errorf("received text = %q, want %q", received[0].Text, "hello")
	}
	mu.Unlock()
}

// A Claude reply must be captured to the event log only when it actually
// reached Slack — capturing a reply whose PostToThread failed would make the
// "conversation timeline" claim an outbound message that was never delivered.
func TestSocketPool_CaptureGatedOnPostSuccess(t *testing.T) {
	run := func(postErr error) (captureCount, postCount int) {
		socketPath := filepath.Join(t.TempDir(), "test.sock")
		listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
			if env.Type == protocol.MsgMessage {
				data, _ := protocol.NewEnvelope(protocol.MsgReply, protocol.ReplyPayload{Text: "```\nhi\n```", ThreadTS: "111.0"})
				writeFakeMessage(conn, data)
			}
		}, testLogger())
		if err != nil {
			t.Fatalf("NewSocketListener: %v", err)
		}
		defer listener.Close()
		go listener.Serve()

		var captureMu sync.Mutex
		captures := 0
		poster := &mockPoster{err: postErr}
		pool := NewSocketPool(poster, testLogger(), func(threadTS, eventType, body string) {
			captureMu.Lock()
			captures++
			captureMu.Unlock()
		}, nil)
		defer pool.Close()

		_ = pool.Send(socketPath, "C01", protocol.MessagePayload{Text: "go", ThreadTS: "111.0"})

		// Wait until the reply round-trips and the post attempt is recorded.
		deadline := time.After(2 * time.Second)
		for poster.postCount() == 0 {
			select {
			case <-deadline:
				t.Fatal("timeout waiting for reply post attempt")
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		time.Sleep(50 * time.Millisecond) // let any (erroneous) capture run
		captureMu.Lock()
		defer captureMu.Unlock()
		return captures, poster.postCount()
	}

	if c, p := run(nil); c != 1 || p != 1 {
		t.Errorf("post success: captures=%d posts=%d, want 1/1", c, p)
	}
	if c, p := run(errors.New("slack down")); c != 0 || p != 1 {
		t.Errorf("post failure: captures=%d posts=%d, want 0/1 (no capture on failed delivery)", c, p)
	}
}

func TestSocketPool_ReplyPostsToSlack(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			reply := protocol.ReplyPayload{
				Text:     "```\nreply\n```",
				ThreadTS: "1234567890.123456",
			}
			data, _ := protocol.NewEnvelope(protocol.MsgReply, reply)
			writeFakeMessage(conn, data)
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{}
	router := NewSocketPool(poster, testLogger(), nil, nil)
	defer router.Close()

	_ = router.Send(socketPath, "C01TEST", protocol.MessagePayload{
		User: "test", Text: "hello", ThreadTS: "1234567890.123456",
	})

	// Wait for reply to be posted to Slack
	deadline := time.After(2 * time.Second)
	for {
		poster.mu.Lock()
		n := len(poster.posts)
		poster.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for Slack post")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	poster.mu.Lock()
	if poster.posts[0].text != "```\nreply\n```" {
		t.Errorf("posted text = %q", poster.posts[0].text)
	}
	if poster.posts[0].channelID != "C01TEST" {
		t.Errorf("posted channelID = %q, want %q", poster.posts[0].channelID, "C01TEST")
	}
	poster.mu.Unlock()
}

func TestSocketPool_ReplyClearsThreadStatus(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			data, _ := protocol.NewEnvelope(protocol.MsgReply, protocol.ReplyPayload{Text: "done", ThreadTS: "111.0"})
			writeFakeMessage(conn, data)
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{}
	statusMgr := NewStatusManager(poster, time.Hour, testLogger())
	defer statusMgr.Stop()
	router := NewSocketPool(poster, testLogger(), nil, statusMgr)
	defer router.Close()

	_ = router.Send(socketPath, "C01TEST", protocol.MessagePayload{Text: "go", ThreadTS: "111.0"})

	deadline := time.After(2 * time.Second)
	for poster.statusCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for status clear")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	poster.mu.Lock()
	defer poster.mu.Unlock()
	if got := poster.statusCalls[0]; got.channelID != "C01TEST" || got.threadTS != "111.0" || got.status != "" {
		t.Errorf("status clear call = %+v, want C01TEST/111.0/empty", got)
	}
}

func TestSocketPool_PermissionPromptClearsThreadStatus(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			data, _ := protocol.NewEnvelope(protocol.MsgPermission, protocol.PermissionPayload{Text: "allow?", ThreadTS: "111.0"})
			writeFakeMessage(conn, data)
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{}
	statusMgr := NewStatusManager(poster, time.Hour, testLogger())
	defer statusMgr.Stop()
	router := NewSocketPool(poster, testLogger(), nil, statusMgr)
	defer router.Close()

	_ = router.Send(socketPath, "C01TEST", protocol.MessagePayload{Text: "go", ThreadTS: "111.0"})

	deadline := time.After(2 * time.Second)
	for poster.statusCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for status clear")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	poster.mu.Lock()
	defer poster.mu.Unlock()
	if got := poster.statusCalls[0]; got.status != "" {
		t.Errorf("status clear call = %+v, want empty status", got)
	}
}

func TestSocketPool_ReplyPostFailureDoesNotClearThreadStatus(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgMessage {
			data, _ := protocol.NewEnvelope(protocol.MsgReply, protocol.ReplyPayload{Text: "done", ThreadTS: "111.0"})
			writeFakeMessage(conn, data)
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{err: errors.New("slack down")}
	statusMgr := NewStatusManager(poster, time.Hour, testLogger())
	defer statusMgr.Stop()
	router := NewSocketPool(poster, testLogger(), nil, statusMgr)
	defer router.Close()

	_ = router.Send(socketPath, "C01TEST", protocol.MessagePayload{Text: "go", ThreadTS: "111.0"})

	deadline := time.After(2 * time.Second)
	for poster.postCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for reply post attempt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond) // let any (erroneous) clear run
	if got := poster.statusCallCount(); got != 0 {
		t.Errorf("SetThreadStatus calls = %d, want 0 (post failed, nothing reached Slack)", got)
	}
}

func TestSocketPool_SendToInvalidSocket(t *testing.T) {
	poster := &mockPoster{}
	router := NewSocketPool(poster, testLogger(), nil, nil)
	defer router.Close()

	err := router.Send("/nonexistent/path.sock", "C01TEST", protocol.MessagePayload{
		ThreadTS: "1234.5678",
		Text:     "hello",
	})
	if err == nil {
		t.Error("Send() should return error for invalid socket path")
	}
}

func TestSocketPool_ReusesConnection(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	var connectCount int
	var mu sync.Mutex

	listener, err := newFakeSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		if env.Type == protocol.MsgRegister {
			mu.Lock()
			connectCount++
			mu.Unlock()
		}
	}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()
	go listener.Serve()

	poster := &mockPoster{}
	router := NewSocketPool(poster, testLogger(), nil, nil)
	defer router.Close()

	// Send two messages to the same socket
	msg := protocol.MessagePayload{User: "test", Text: "hello", ThreadTS: "1234.5678"}
	if err := router.Send(socketPath, "C01TEST", msg); err != nil {
		t.Fatalf("first Send() error: %v", err)
	}
	if err := router.Send(socketPath, "C01TEST", msg); err != nil {
		t.Fatalf("second Send() error: %v", err)
	}

	// Should have only connected once (Register is sent once per connection)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if connectCount != 1 {
		t.Errorf("expected 1 connection, got %d", connectCount)
	}
	mu.Unlock()
}
