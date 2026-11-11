package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type protocolMessage struct {
	Op        string          `json:"op"`
	ID        string          `json:"id"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Delivered int             `json:"delivered"`
	Config    json.RawMessage `json:"config"`
}

type ServiceProtocol struct {
	input     *bufio.Scanner
	output    *json.Encoder
	outputMu  sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan protocolMessage
	nextID    atomic.Uint64
	done      chan struct{}
	readErr   error
}

func NewServiceProtocol(input io.Reader, output io.Writer) *ServiceProtocol {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 10*1024*1024)
	return &ServiceProtocol{input: scanner, output: json.NewEncoder(output), pending: make(map[string]chan protocolMessage), done: make(chan struct{})}
}

func (p *ServiceProtocol) read() (protocolMessage, error) {
	if !p.input.Scan() {
		if err := p.input.Err(); err != nil {
			return protocolMessage{}, err
		}
		return protocolMessage{}, io.EOF
	}
	var msg protocolMessage
	if err := json.Unmarshal(p.input.Bytes(), &msg); err != nil {
		return protocolMessage{}, err
	}
	return msg, nil
}

func (p *ServiceProtocol) ReadConfig() ([]byte, error) {
	msg, err := p.read()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if msg.Op != "config" || msg.Config == nil {
		return nil, fmt.Errorf("first service message must be config")
	}
	return msg.Config, nil
}

func (p *ServiceProtocol) ReadSnapshot() error {
	msg, err := p.read()
	if err != nil {
		return fmt.Errorf("read subscriptions snapshot: %w", err)
	}
	if msg.Op != "subscriptions" {
		return fmt.Errorf("second service message must be subscriptions")
	}
	return nil
}

func (p *ServiceProtocol) ReadReplies() error {
	defer close(p.done)
	for {
		msg, err := p.read()
		if err != nil {
			p.readErr = err
			return err
		}
		switch msg.Op {
		case "subscriptions":
			continue
		case "published", "error":
			p.pendingMu.Lock()
			ch := p.pending[msg.ID]
			delete(p.pending, msg.ID)
			p.pendingMu.Unlock()
			if ch != nil {
				ch <- msg
			}
		default:
			p.readErr = fmt.Errorf("unknown service message %q", msg.Op)
			return p.readErr
		}
	}
}

func (p *ServiceProtocol) Publish(ctx context.Context, resource, eventType, summary, body string, metadata map[string]string) (int, error) {
	id := fmt.Sprintf("slack-%d", p.nextID.Add(1))
	ch := make(chan protocolMessage, 1)
	p.pendingMu.Lock()
	p.pending[id] = ch
	p.pendingMu.Unlock()
	defer func() { p.pendingMu.Lock(); delete(p.pending, id); p.pendingMu.Unlock() }()
	p.outputMu.Lock()
	err := p.output.Encode(map[string]any{"op": "publish", "id": id, "resource": resource, "type": eventType, "summary": summary, "body": body, "metadata": metadata})
	p.outputMu.Unlock()
	if err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.done:
		return 0, fmt.Errorf("service protocol closed: %w", p.readErr)
	case reply := <-ch:
		if reply.Op == "error" {
			return 0, fmt.Errorf("%s: %s", reply.Code, reply.Message)
		}
		return reply.Delivered, nil
	}
}
