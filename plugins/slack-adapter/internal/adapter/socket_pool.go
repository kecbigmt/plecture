package adapter

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/kecbigmt/plect/contracts/channel-protocol"
)

// SocketPool manages connections to channel-server instances via Unix socket.
// Connections are keyed by socket_path and reused across messages.
type SocketPool struct {
	mu     sync.RWMutex
	conns  map[string]*socketConn // socket_path -> connection
	poster ThreadPoster
	logger *slog.Logger
	// capture, if set, records an outbound message (Claude reply / permission
	// prompt) the channel-server sent back, keyed by thread_ts. Optional (nil in
	// tests that don't exercise capture).
	capture func(threadTS, eventType, body string)
}

// socketConn holds a connection and its associated metadata.
type socketConn struct {
	client    *SocketClient
	channelID string
}

// ThreadPoster posts messages to Slack threads.
type ThreadPoster interface {
	PostToThread(channelID, threadTS, text string) (string, error)
}

// NewSocketPool creates a new socket connection pool.
func NewSocketPool(poster ThreadPoster, logger *slog.Logger, capture func(threadTS, eventType, body string)) *SocketPool {
	return &SocketPool{
		conns:   make(map[string]*socketConn),
		poster:  poster,
		logger:  logger,
		capture: capture,
	}
}

// Send sends a message to the channel-server at the given socket path.
// It reuses an existing connection or creates a new one.
func (sr *SocketPool) Send(socketPath, channelID string, msg protocol.MessagePayload) error {
	conn, err := sr.getOrConnect(socketPath, channelID, msg.ThreadTS)
	if err != nil {
		return err
	}

	if err := conn.client.SendMessage(msg); err != nil {
		sr.logger.Error("send failed, removing connection", "socket_path", socketPath, "error", err)
		sr.mu.Lock()
		if cur, ok := sr.conns[socketPath]; ok && cur == conn {
			conn.client.Close()
			delete(sr.conns, socketPath)
		}
		sr.mu.Unlock()
		return err
	}
	return nil
}

// getOrConnect returns an existing connection or creates a new one.
func (sr *SocketPool) getOrConnect(socketPath, channelID, threadTS string) (*socketConn, error) {
	sr.mu.RLock()
	conn, ok := sr.conns[socketPath]
	sr.mu.RUnlock()
	if ok {
		return conn, nil
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Double-check after acquiring write lock
	if conn, ok := sr.conns[socketPath]; ok {
		return conn, nil
	}

	client, err := NewSocketClient(socketPath, threadTS, channelID, sr.logger,
		func(reply protocol.ReplyPayload) {
			if _, err := sr.poster.PostToThread(channelID, reply.ThreadTS, reply.Text); err != nil {
				sr.logger.Error("failed to post reply to Slack", "error", err)
				return // don't log an outbound event that never reached Slack
			}
			if sr.capture != nil {
				sr.capture(reply.ThreadTS, "claude.reply", reply.Text)
			}
		},
		func(perm protocol.PermissionPayload) {
			if _, err := sr.poster.PostToThread(channelID, perm.ThreadTS, perm.Text); err != nil {
				sr.logger.Error("failed to post permission to Slack", "error", err)
				return // don't log an outbound event that never reached Slack
			}
			if sr.capture != nil {
				sr.capture(perm.ThreadTS, "claude.permission_request", perm.Text)
			}
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to channel-server: %w", err)
	}

	conn = &socketConn{client: client, channelID: channelID}
	sr.conns[socketPath] = conn

	// Start read loop in background
	go func() {
		client.ReadLoop()
		sr.mu.Lock()
		if cur, ok := sr.conns[socketPath]; ok && cur == conn {
			delete(sr.conns, socketPath)
			sr.logger.Info("connection closed", "socket_path", socketPath)
		}
		sr.mu.Unlock()
	}()

	sr.logger.Info("connected", "socket_path", socketPath)
	return conn, nil
}

// Connect establishes a connection to a channel-server socket without sending a message.
// Used for pre-warming connections on startup.
func (sr *SocketPool) Connect(socketPath, channelID, threadTS string) error {
	_, err := sr.getOrConnect(socketPath, channelID, threadTS)
	return err
}

// Close disconnects all clients.
func (sr *SocketPool) Close() {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	for path, conn := range sr.conns {
		conn.client.Close()
		delete(sr.conns, path)
	}
}
