package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/kecbigmt/sennit/contracts/channel-protocol"
)

// MessageHandler is called when a message is received from a connected client.
type MessageHandler func(env protocol.Envelope, conn net.Conn)

// SocketListener listens on a Unix socket and dispatches incoming messages.
type SocketListener struct {
	path         string
	listener     net.Listener
	handler      MessageHandler
	OnDisconnect func(conn net.Conn)
	mu           sync.Mutex
	closed       bool
	logger       *slog.Logger
}

// NewSocketListener creates and starts listening on the given Unix socket path.
func NewSocketListener(path string, handler MessageHandler, logger *slog.Logger) (*SocketListener, error) {
	// Remove stale socket file if it exists
	os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", path, err)
	}

	return &SocketListener{
		path:     path,
		listener: ln,
		handler:  handler,
		logger:   logger,
	}, nil
}

// Serve accepts connections and handles them. Blocks until Close() is called.
func (s *SocketListener) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.logger.Error("accept error", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *SocketListener) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		if s.OnDisconnect != nil {
			s.OnDisconnect(conn)
		}
	}()

	for {
		data, err := readMessage(conn)
		if err != nil {
			if err != io.EOF {
				s.logger.Error("read error", "error", err)
			}
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.logger.Error("unmarshal error", "error", err)
			continue
		}

		s.handler(env, conn)
	}
}

// Close stops the listener and removes the socket file.
func (s *SocketListener) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	err := s.listener.Close()
	os.Remove(s.path)
	return err
}

// WriteMessageTo writes a length-prefixed message to a connection (exported for cross-package use).
func WriteMessageTo(conn net.Conn, data []byte) error {
	return writeMessage(conn, data)
}

// writeMessage writes a length-prefixed message to a connection.
// Format: 4-byte big-endian length + payload.
func writeMessage(conn net.Conn, data []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

// readMessage reads a length-prefixed message from a connection.
func readMessage(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	if length > 1<<20 { // 1MB max
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}
