package adapter

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"

	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
)

// fakeSocketListener is a protocol-conformant stand-in for channel-server's
// own Unix-socket listener, owned by this module instead of imported from
// it (docs/adr/2026-08-17-plugin-boundary-contracts.md: chat-delivery
// plugins do not import channel-server client packages — only the wire
// contract in contracts/channel-protocol is shared). Framing and behavior
// mirror channel-server/server/socket_listener.go exactly, since these
// tests exist to prove SocketClient speaks that same protocol correctly;
// diverging from it here would let a protocol regression pass unnoticed.
type fakeSocketListener struct {
	path         string
	listener     net.Listener
	handler      func(env protocol.Envelope, conn net.Conn)
	onDisconnect func(conn net.Conn)
	mu           sync.Mutex
	closed       bool
	logger       *slog.Logger
}

func newFakeSocketListener(path string, handler func(protocol.Envelope, net.Conn), logger *slog.Logger) (*fakeSocketListener, error) {
	os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", path, err)
	}

	return &fakeSocketListener{
		path:     path,
		listener: ln,
		handler:  handler,
		logger:   logger,
	}, nil
}

func (s *fakeSocketListener) Serve() {
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

func (s *fakeSocketListener) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		if s.onDisconnect != nil {
			s.onDisconnect(conn)
		}
	}()

	for {
		data, err := readFakeMessage(conn)
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

func (s *fakeSocketListener) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	err := s.listener.Close()
	os.Remove(s.path)
	return err
}

// writeFakeMessage writes a length-prefixed message to a connection —
// exported for cross-test-file use within this package, mirroring
// channel-server's WriteMessageTo.
func writeFakeMessage(conn net.Conn, data []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

func readFakeMessage(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	if length > 1<<20 { // 1MB max, matching channel-server's own cap
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}
