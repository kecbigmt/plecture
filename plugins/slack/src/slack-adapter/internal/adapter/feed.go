package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type feedClient struct {
	conn     net.Conn
	channels map[string]bool
}

type MentionFeed struct {
	mu       sync.Mutex
	listener net.Listener
	clients  []feedClient
	coverage *Coverage
}

func NewMentionFeed(path string, coverage ...*Coverage) (*MentionFeed, error) {
	if path == "" {
		return nil, fmt.Errorf("feed socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("feed path %q exists and is not a socket", path)
		}
		if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
			conn.Close()
			return nil, fmt.Errorf("feed socket %q is already in use", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	f := &MentionFeed{listener: listener}
	if len(coverage) > 0 {
		f.coverage = coverage[0]
	}
	return f, nil
}

func (f *MentionFeed) AcceptOne() error {
	conn, err := f.listener.Accept()
	if err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registration struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.NewDecoder(conn).Decode(&registration); err != nil {
		conn.Close()
		return err
	}
	conn.SetReadDeadline(time.Time{})
	if len(registration.ChannelIDs) == 0 {
		conn.Close()
		return fmt.Errorf("feed registration requires channel_ids")
	}
	client := feedClient{conn: conn, channels: make(map[string]bool)}
	for _, channel := range registration.ChannelIDs {
		if !slackChannelID.MatchString(channel) {
			conn.Close()
			return fmt.Errorf("invalid feed channel_id %q", channel)
		}
		client.channels[channel] = true
	}
	if f.coverage != nil {
		owner := f.coverage.Owner(registration.ChannelIDs[0])
		if owner == "" || !f.coverage.Matches(owner, registration.ChannelIDs) {
			conn.Close()
			return fmt.Errorf("feed registration does not match an active population query")
		}
	}
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	return nil
}

func (f *MentionFeed) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); f.Close() }()
	for ctx.Err() == nil {
		if err := f.AcceptOne(); err != nil && ctx.Err() == nil {
			slog.Warn("mention feed connection rejected", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return nil
}

func (f *MentionFeed) Emit(mention PendingMention) bool {
	item := struct {
		Resource  string `json:"resource"`
		ChannelID string `json:"channel_id"`
		ThreadTS  string `json:"thread_ts"`
		MentionTS string `json:"mention_ts"`
		UserID    string `json:"user_id"`
	}{mention.Resource, mention.ChannelID, mention.ThreadTS, mention.MentionTS, mention.UserID}
	f.mu.Lock()
	defer f.mu.Unlock()
	delivered := false
	kept := f.clients[:0]
	for _, client := range f.clients {
		if !client.channels[mention.ChannelID] {
			kept = append(kept, client)
			continue
		}
		client.conn.SetWriteDeadline(time.Now().Add(time.Second))
		if err := json.NewEncoder(client.conn).Encode(item); err != nil {
			client.conn.Close()
			continue
		}
		kept = append(kept, client)
		delivered = true
	}
	f.clients = kept
	return delivered
}

func (f *MentionFeed) Close() error {
	f.DropClients()
	return f.listener.Close()
}

func (f *MentionFeed) DropClients() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, client := range f.clients {
		client.conn.Close()
	}
	f.clients = nil
}

func StreamMentions(ctx context.Context, path string, channels []string, output io.Writer) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]any{"channel_ids": channels}); err != nil {
		return err
	}
	go func() { <-ctx.Done(); conn.Close() }()
	_, err = io.Copy(output, conn)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("mention feed ended")
}
