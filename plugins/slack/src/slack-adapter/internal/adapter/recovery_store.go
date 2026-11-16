package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const recoveryLookback = 24 * time.Hour
const recoveryOverlap = 5 * time.Minute

type recoveryFile struct {
	Version  int                        `json:"version"`
	Channels map[string]recoveryChannel `json:"channels"`
}
type recoveryChannel struct {
	Boundary  time.Time       `json:"boundary"`
	Started   time.Time       `json:"started"`
	Processed map[string]bool `json:"processed"`
}

type RecoveryStore struct {
	mu    sync.Mutex
	path  string
	state recoveryFile
}

func NewRecoveryStore(dataDir string) (*RecoveryStore, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("PLECT_DATA_DIR is required")
	}
	s := &RecoveryStore{path: filepath.Join(dataDir, "recovery.json"), state: recoveryFile{Version: 1, Channels: make(map[string]recoveryChannel)}}
	data, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("load recovery state: %w", err)
		}
		if s.state.Version != 1 || s.state.Channels == nil {
			return nil, fmt.Errorf("unsupported recovery state version %d", s.state.Version)
		}
	}
	return s, nil
}

func (s *RecoveryStore) Boundary(key string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Channels[key].Boundary
}
func (s *RecoveryStore) Started(key string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Channels[key].Started
}
func (s *RecoveryStore) Initialize(key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Channels[key].Boundary.IsZero() {
		return nil
	}
	s.state.Channels[key] = recoveryChannel{Boundary: at, Started: at}
	if err := s.save(); err != nil {
		delete(s.state.Channels, key)
		return err
	}
	return nil
}
func (s *RecoveryStore) Seen(key, ts string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Channels[key].Processed[ts]
}
func (s *RecoveryStore) Record(key, ts string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.state.Channels[key]
	if channel.Processed == nil {
		channel.Processed = make(map[string]bool)
	}
	if channel.Processed[ts] {
		return nil
	}
	channel.Processed[ts] = true
	s.state.Channels[key] = channel
	if err := s.save(); err != nil {
		delete(channel.Processed, ts)
		return err
	}
	return nil
}
func (s *RecoveryStore) SetBoundary(key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.state.Channels[key]
	channel := prior
	channel.Boundary = at
	if prior.Processed != nil {
		channel.Processed = make(map[string]bool, len(prior.Processed))
		for ts := range prior.Processed {
			channel.Processed[ts] = true
		}
	}
	cutoff := at.Add(-recoveryLookback - recoveryOverlap)
	for ts := range channel.Processed {
		sec, _, ok := parseSlackTimestamp(ts)
		if ok && time.Unix(sec, 0).Before(cutoff) {
			delete(channel.Processed, ts)
		}
	}
	s.state.Channels[key] = channel
	if err := s.save(); err != nil {
		s.state.Channels[key] = prior
		return err
	}
	return nil
}
func (s *RecoveryStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), "recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
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
