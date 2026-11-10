package watcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

type MembershipResource struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Locator string `json:"locator"`
}
type MembershipRow struct {
	Session    string             `json:"session"`
	Source     string             `json:"source"`
	InputsJSON string             `json:"inputs_json"`
	Resource   MembershipResource `json:"resource"`
}
type resourceState struct {
	Last       map[string]string `json:"last,omitempty"`
	FailStreak int               `json:"fail_streak,omitempty"`
}
type ProtocolStore struct {
	stateFile string
	cacheFile string
	mu        sync.RWMutex
	rows      map[string]MembershipRow
}

func NewProtocolStore(dataDir, cacheDir string) *ProtocolStore {
	return &ProtocolStore{stateFile: filepath.Join(dataDir, "watch-state.json"), cacheFile: filepath.Join(cacheDir, "etag.json"), rows: map[string]MembershipRow{}}
}
func membershipKey(row MembershipRow) string {
	return row.Session + "\x00" + row.Resource.ID + "\x00" + row.Source
}
func (s *ProtocolStore) Replace(rows []MembershipRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = make(map[string]MembershipRow, len(rows))
	for _, row := range rows {
		s.rows[membershipKey(row)] = row
	}
	return nil
}
func (s *ProtocolStore) Apply(add, remove []MembershipRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range remove {
		delete(s.rows, membershipKey(row))
	}
	for _, row := range add {
		s.rows[membershipKey(row)] = row
	}
}
func (s *ProtocolStore) All() (map[string]*Subscription, error) {
	var states map[string]resourceState
	if err := readLockedJSON(s.stateFile, &states); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*Subscription)
	keys := make([]string, 0, len(s.rows))
	for key := range s.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		row := s.rows[key]
		if row.Session == "" || !resourceRE.MatchString(row.Resource.Locator) {
			continue
		}
		id := subKey(row.Session, row.Resource.Locator)
		branch := ""
		var inputs map[string]any
		if json.Unmarshal([]byte(row.InputsJSON), &inputs) == nil {
			branch, _ = inputs["branch"].(string)
		}
		if existing := out[id]; existing != nil {
			if existing.Branch == "" {
				existing.Branch = branch
			}
			continue
		}
		state := states[row.Resource.Locator]
		out[id] = &Subscription{SessionName: row.Session, Resource: row.Resource.Locator, Branch: branch, Last: state.Last, FailStreak: state.FailStreak}
	}
	return out, nil
}
func (s *ProtocolStore) SetLast(_, resource string, last map[string]string) error {
	return updateLockedJSON(s.stateFile, func(raw []byte) ([]byte, error) {
		states, err := decodeStates(raw)
		if err != nil {
			return nil, err
		}
		state := states[resource]
		state.Last = last
		states[resource] = state
		return json.Marshal(states)
	})
}
func (s *ProtocolStore) IncrementFailStreak(_, resource string) (int, error) {
	streak := 0
	err := updateLockedJSON(s.stateFile, func(raw []byte) ([]byte, error) {
		states, err := decodeStates(raw)
		if err != nil {
			return nil, err
		}
		state := states[resource]
		state.FailStreak++
		streak = state.FailStreak
		states[resource] = state
		return json.Marshal(states)
	})
	return streak, err
}
func (s *ProtocolStore) ResetFailStreak(_, resource string) error {
	return updateLockedJSON(s.stateFile, func(raw []byte) ([]byte, error) {
		states, err := decodeStates(raw)
		if err != nil {
			return nil, err
		}
		state := states[resource]
		state.FailStreak = 0
		states[resource] = state
		return json.Marshal(states)
	})
}
func (s *ProtocolStore) GetCache(key string) (string, string, bool) {
	var cache map[string]cacheEntry
	if err := readLockedJSON(s.cacheFile, &cache); err != nil {
		return "", "", false
	}
	entry, ok := cache[key]
	return entry.ETag, entry.Body, ok
}
func (s *ProtocolStore) SetCache(key, etag, body string) error {
	return updateLockedJSON(s.cacheFile, func(raw []byte) ([]byte, error) {
		cache := map[string]cacheEntry{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cache); err != nil {
				return nil, err
			}
		}
		cache[key] = cacheEntry{ETag: etag, Body: body}
		return json.Marshal(cache)
	})
}
func decodeStates(raw []byte) (map[string]resourceState, error) {
	states := map[string]resourceState{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &states); err != nil {
			return nil, err
		}
	}
	return states, nil
}
func readLockedJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
func updateLockedJSON(path string, fn func([]byte) ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated, err := fn(raw)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, updated)
}
