package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type DenialStore struct {
	mu   sync.Mutex
	path string
	keys map[string]bool
}

func NewDenialStore(dataDir string) (*DenialStore, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("PLECT_DATA_DIR is required")
	}
	path := filepath.Join(dataDir, "denials.json")
	store := &DenialStore{path: path, keys: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &store.keys); err != nil {
			return nil, fmt.Errorf("load denial records: %w", err)
		}
	}
	return store, nil
}

func (s *DenialStore) Claim(bot, channel, thread, user string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bot + "/" + channel + "/" + thread + "/" + user
	if s.keys[key] {
		return false, nil
	}
	s.keys[key] = true
	if err := s.save(); err != nil {
		delete(s.keys, key)
		return false, err
	}
	return true, nil
}

func (s *DenialStore) Release(bot, channel, thread, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, bot+"/"+channel+"/"+thread+"/"+user)
	return s.save()
}

func (s *DenialStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(s.keys)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "denials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
