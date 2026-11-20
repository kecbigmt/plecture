package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type PendingMention struct {
	Key       string            `json:"key"`
	Resource  string            `json:"resource"`
	ChannelID string            `json:"channel_id"`
	ThreadTS  string            `json:"thread_ts"`
	MentionTS string            `json:"mention_ts"`
	UserID    string            `json:"user_id"`
	Summary   string            `json:"summary"`
	Body      string            `json:"body"`
	Metadata  map[string]string `json:"metadata"`
}

type PendingStore struct {
	mu   sync.Mutex
	path string
	rows map[string]PendingMention
}

func NewPendingStore(dir string) (*PendingStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("PLECT_DATA_DIR is required")
	}
	s := &PendingStore{path: filepath.Join(dir, "pending-mentions.json"), rows: make(map[string]PendingMention)}
	data, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &s.rows); err != nil || s.rows == nil {
			return nil, fmt.Errorf("load pending mentions: invalid state: %v", err)
		}
	}
	return s, nil
}

func (s *PendingStore) Put(mention PendingMention) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mention.Key == "" {
		return fmt.Errorf("pending mention key is required")
	}
	if _, exists := s.rows[mention.Key]; exists {
		return nil
	}
	s.rows[mention.Key] = mention
	if err := s.save(); err != nil {
		delete(s.rows, mention.Key)
		return err
	}
	return nil
}

func (s *PendingStore) List() []PendingMention {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.rows))
	for key := range s.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]PendingMention, 0, len(keys))
	for _, key := range keys {
		out = append(out, s.rows[key])
	}
	return out
}

func (s *PendingStore) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rows[key]
	return ok
}

func (s *PendingStore) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, exists := s.rows[key]
	if !exists {
		return nil
	}
	delete(s.rows, key)
	if err := s.save(); err != nil {
		s.rows[key] = prior
		return err
	}
	return nil
}

func (s *PendingStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.rows)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), "pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
