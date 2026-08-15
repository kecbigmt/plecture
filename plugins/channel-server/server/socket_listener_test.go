package server

import (
	"encoding/json"
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

func TestSocketListener_AcceptAndReceiveMessage(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	var received []protocol.Envelope
	var mu sync.Mutex

	handler := func(env protocol.Envelope, conn net.Conn) {
		mu.Lock()
		received = append(received, env)
		mu.Unlock()
	}

	listener, err := NewSocketListener(socketPath, handler, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()

	go listener.Serve()

	// Connect as client and send a message
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer conn.Close()

	msg := protocol.MessagePayload{
		User:     "testuser",
		UserID:   "U123",
		Text:     "hello from slack",
		ThreadTS: "1234567890.123456",
	}
	envData, err := protocol.NewEnvelope(protocol.MsgMessage, msg)
	if err != nil {
		t.Fatalf("NewEnvelope() error: %v", err)
	}

	// Write length-prefixed message
	if err := writeMessage(conn, envData); err != nil {
		t.Fatalf("writeMessage() error: %v", err)
	}

	// Wait for message to be processed
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
			t.Fatal("timeout waiting for message")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 message, got %d", len(received))
	}
	if received[0].Type != protocol.MsgMessage {
		t.Errorf("Type = %q, want %q", received[0].Type, protocol.MsgMessage)
	}
}

func TestSocketListener_MultipleClients(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	var received []protocol.Envelope
	var mu sync.Mutex

	handler := func(env protocol.Envelope, conn net.Conn) {
		mu.Lock()
		received = append(received, env)
		mu.Unlock()
	}

	listener, err := NewSocketListener(socketPath, handler, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()

	go listener.Serve()

	// Connect two clients
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Errorf("Dial() client %d error: %v", id, err)
				return
			}
			defer conn.Close()

			msg := protocol.MessagePayload{
				User: "user" + string(rune('0'+id)),
				Text: "hello",
			}
			data, _ := protocol.NewEnvelope(protocol.MsgMessage, msg)
			writeMessage(conn, data)
		}(i)
	}
	wg.Wait()

	// Wait for messages
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout: received %d messages, want 2", len(received))
			mu.Unlock()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestSocketListener_SendReply(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	handler := func(env protocol.Envelope, conn net.Conn) {
		// Echo back a reply
		reply := protocol.ReplyPayload{
			Text:     "reply text",
			ThreadTS: "1234567890.123456",
		}
		data, _ := protocol.NewEnvelope(protocol.MsgReply, reply)
		writeMessage(conn, data)
	}

	listener, err := NewSocketListener(socketPath, handler, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	defer listener.Close()

	go listener.Serve()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer conn.Close()

	// Send a message to trigger the reply
	msg := protocol.MessagePayload{User: "test", Text: "hello"}
	data, _ := protocol.NewEnvelope(protocol.MsgMessage, msg)
	writeMessage(conn, data)

	// Read the reply
	replyData, err := readMessage(conn)
	if err != nil {
		t.Fatalf("readMessage() error: %v", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(replyData, &env); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if env.Type != protocol.MsgReply {
		t.Errorf("Type = %q, want %q", env.Type, protocol.MsgReply)
	}
}

func TestSocketListener_CleanupOnClose(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	listener, err := NewSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}

	go listener.Serve()

	// Socket file should exist
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}

	listener.Close()

	// Socket file should be removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after Close()")
	}
}

func TestSocketListener_ClientDisconnect(t *testing.T) {
	socketDir := t.TempDir()
	socketPath := filepath.Join(socketDir, "test.sock")

	disconnected := make(chan struct{}, 1)

	listener, err := NewSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {}, testLogger())
	if err != nil {
		t.Fatalf("NewSocketListener() error: %v", err)
	}
	listener.OnDisconnect = func(conn net.Conn) {
		disconnected <- struct{}{}
	}
	defer listener.Close()

	go listener.Serve()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}

	// Close the client connection
	conn.Close()

	select {
	case <-disconnected:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for disconnect callback")
	}
}
