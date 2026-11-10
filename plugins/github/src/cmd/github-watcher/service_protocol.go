package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/watcher"
)

type serviceMessage struct {
	Op      string                  `json:"op"`
	ID      string                  `json:"id"`
	Code    string                  `json:"code"`
	Message string                  `json:"message"`
	Config  map[string]any          `json:"config"`
	Add     []watcher.MembershipRow `json:"add"`
	Remove  []watcher.MembershipRow `json:"remove"`
}
type serviceProtocol struct {
	input     *bufio.Scanner
	output    *json.Encoder
	outputMu  sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan serviceMessage
	nextID    atomic.Uint64
	store     *watcher.ProtocolStore
	ready     chan struct{}
}

func newServiceProtocol(input io.Reader, output io.Writer, store *watcher.ProtocolStore) *serviceProtocol {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 10*1024*1024)
	return &serviceProtocol{input: scanner, output: json.NewEncoder(output), pending: map[string]chan serviceMessage{}, store: store, ready: make(chan struct{})}
}
func (p *serviceProtocol) readMessage() (serviceMessage, error) {
	if !p.input.Scan() {
		if err := p.input.Err(); err != nil {
			return serviceMessage{}, err
		}
		return serviceMessage{}, io.EOF
	}
	var message serviceMessage
	if err := json.Unmarshal(p.input.Bytes(), &message); err != nil {
		return serviceMessage{}, err
	}
	return message, nil
}
func (p *serviceProtocol) Run() error {
	first, err := p.readMessage()
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if first.Op != "config" || first.Config == nil {
		return fmt.Errorf("first service message must be config")
	}
	snapshot, err := p.readMessage()
	if err != nil {
		return fmt.Errorf("read subscriptions snapshot: %w", err)
	}
	if snapshot.Op != "subscriptions" {
		return fmt.Errorf("second service message must be subscriptions")
	}
	if err := p.store.Replace(snapshot.Add); err != nil {
		return err
	}
	close(p.ready)
	for {
		message, err := p.readMessage()
		if err != nil {
			return err
		}
		switch message.Op {
		case "subscriptions":
			p.store.Apply(message.Add, message.Remove)
		case "published", "error":
			p.pendingMu.Lock()
			ch := p.pending[message.ID]
			delete(p.pending, message.ID)
			p.pendingMu.Unlock()
			if ch != nil {
				ch <- message
			}
		default:
			return fmt.Errorf("unknown service message %q", message.Op)
		}
	}
}
func (p *serviceProtocol) Publish(ctx context.Context, resource, eventType, summary string, metadata map[string]string) error {
	id := fmt.Sprintf("github-%d", p.nextID.Add(1))
	ch := make(chan serviceMessage, 1)
	p.pendingMu.Lock()
	p.pending[id] = ch
	p.pendingMu.Unlock()
	defer func() { p.pendingMu.Lock(); delete(p.pending, id); p.pendingMu.Unlock() }()
	p.outputMu.Lock()
	err := p.output.Encode(map[string]any{"op": "publish", "id": id, "resource": resource, "type": eventType, "summary": summary, "metadata": metadata})
	p.outputMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply := <-ch:
		if reply.Op == "error" {
			return fmt.Errorf("%s: %s", reply.Code, reply.Message)
		}
		return nil
	}
}
