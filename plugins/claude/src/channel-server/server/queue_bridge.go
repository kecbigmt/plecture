package server

import (
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kecbigmt/plecture/contracts/channel-protocol"
)

// NewQueueBridge listens on socketPath with the same framing a channel-server
// serves, but turns each message into a queued turn file in queueDir instead
// of an MCP notification: a print-mode runtime has no live session to notify,
// only a worker draining that directory between turns. An adapter therefore
// connects to either shape without knowing which runtime is behind it.
//
// A "y <id>" message is queued as ordinary text: a print-mode turn never
// raises a permission request, so there is no live nonce for it to answer.
func NewQueueBridge(socketPath, queueDir string, logger *slog.Logger) (*SocketListener, error) {
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		return nil, fmt.Errorf("create queue dir %s: %w", queueDir, err)
	}
	q := &turnQueue{dir: queueDir}
	return NewSocketListener(socketPath, func(env protocol.Envelope, _ net.Conn) {
		if env.Type != protocol.MsgMessage {
			return
		}
		var msg protocol.MessagePayload
		if err := env.UnmarshalPayload(&msg); err != nil {
			logger.Error("failed to unmarshal message", "error", err)
			return
		}
		if msg.Text == "" {
			return
		}
		if err := q.enqueue(renderChannelTurn(msg)); err != nil {
			logger.Error("failed to enqueue message", "error", err)
		}
	}, logger)
}

// renderChannelTurn wraps the text in the same <channel> tag an interactive
// session shows for a channel notification, so instructions written against
// that shape read a queued turn the same way.
func renderChannelTurn(msg protocol.MessagePayload) string {
	var b strings.Builder
	b.WriteString(`<channel source="channel-server"`)
	for _, attr := range []struct{ key, value string }{
		{"user", msg.User},
		{"user_id", msg.UserID},
		{"thread_ts", msg.ThreadTS},
	} {
		if attr.value != "" {
			fmt.Fprintf(&b, ` %s="%s"`, attr.key, html.EscapeString(attr.value))
		}
	}
	b.WriteString(">\n")
	b.WriteString(msg.Text)
	b.WriteString("\n</channel>")
	return b.String()
}

type turnQueue struct {
	dir  string
	mu   sync.Mutex
	last int64
}

// enqueue names each file after a strictly increasing nanosecond stamp, since
// the worker drains in filename order and two messages can land within one
// clock tick. The rename makes a half-written file invisible to that drain.
func (q *turnQueue) enqueue(text string) error {
	data, err := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"channel.message", text})
	if err != nil {
		return err
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	stamp := time.Now().UnixNano()
	if stamp <= q.last {
		stamp = q.last + 1
	}
	q.last = stamp

	tmp, err := os.CreateTemp(q.dir, ".bridge.*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(q.dir, fmt.Sprintf("%d-%d.json", stamp, os.Getpid())))
}
