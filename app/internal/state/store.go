package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/cradel-dev/cradel/app/internal/domain"
	"github.com/cradel-dev/cradel/contracts/atomicfile"
	contract "github.com/cradel-dev/cradel/contracts/state"
)

const stateVersion = 5

type stateFile struct {
	Version  int                        `json:"version"`
	Sessions map[string]*domain.Session `json:"sessions"`
}

// Store manages session state persistence.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a Store using the given directory for state.json.
// If dir is empty, defaults to ~/.local/share/sennit.
func NewStore(dir string) *Store {
	if dir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			home, _ := os.UserHomeDir()
			dataHome = filepath.Join(home, ".local", "share")
		}
		dir = filepath.Join(dataHome, "sennit")
	}
	return &Store{path: filepath.Join(dir, "state.json")}
}

// Dir returns the directory holding state.json. Co-located stores (e.g. the
// event log) derive their root from it so they share the same data home.
func (s *Store) Dir() string {
	return filepath.Dir(s.path)
}

// Get returns a session by name, or nil if not found.
func (s *Store) Get(name string) *domain.Session {
	sf := s.load()
	return sf.Sessions[name]
}

// Put saves or updates a session.
func (s *Store) Put(session *domain.Session) error {
	return s.withFileLock(func() error {
		sf, err := s.loadLocked()
		if err != nil {
			return fmt.Errorf("state: put %q: %w", session.Name, err)
		}
		sf.Sessions[session.Name] = session
		normalizeSessionTree(sf.Sessions)
		return s.saveLocked(sf)
	})
}

// Update atomically applies fn to the named session under the file lock and
// persists the result. This is the read-modify-write primitive for callers
// that may race with other sennit processes (e.g. a watcher daemon merging
// task outputs while a lifecycle command runs). fn returning an error
// aborts without writing.
func (s *Store) Update(name string, fn func(*domain.Session) error) error {
	return s.withFileLock(func() error {
		sf, err := s.loadLocked()
		if err != nil {
			return fmt.Errorf("state: update %q: %w", name, err)
		}
		session, ok := sf.Sessions[name]
		if !ok {
			return fmt.Errorf("no state entry for session %q", name)
		}
		if err := fn(session); err != nil {
			return err
		}
		normalizeSessionTree(sf.Sessions)
		return s.saveLocked(sf)
	})
}

// Delete removes a session by name.
func (s *Store) Delete(name string) error {
	return s.withFileLock(func() error {
		sf, err := s.loadLocked()
		if err != nil {
			return fmt.Errorf("state: delete %q: %w", name, err)
		}
		for _, session := range sf.Sessions {
			if session == nil {
				continue
			}
			if session.ParentSession == name {
				session.ParentSession = ""
			}
			session.Children = removeSessionName(session.Children, name)
		}
		delete(sf.Sessions, name)
		normalizeSessionTree(sf.Sessions)
		return s.saveLocked(sf)
	})
}

// FindByAlias returns all sessions whose create-time alias equals the given
// string. Multiple hits are possible (tag variants share the alias), so the
// caller decides how to disambiguate.
func (s *Store) FindByAlias(alias string) []*domain.Session {
	sf := s.load()
	var result []*domain.Session
	for _, session := range sf.Sessions {
		if session.Alias != "" && session.Alias == alias {
			result = append(result, session)
		}
	}
	return result
}

// All returns all sessions.
func (s *Store) All() map[string]*domain.Session {
	sf := s.load()
	result := make(map[string]*domain.Session, len(sf.Sessions))
	for k, v := range sf.Sessions {
		result[k] = v
	}
	return result
}

// withFileLock acquires both the in-process mutex and a file-level lock (flock)
// to provide cross-process mutual exclusion for read-modify-write operations.
func (s *Store) withFileLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lockPath := s.path + ".lock"
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

// withSharedFileLock acquires the in-process mutex and a shared file lock (LOCK_SH)
// to prevent reading a partially-written file from another process.
func (s *Store) withSharedFileLock(fn func() *stateFile) *stateFile {
	s.mu.Lock()
	defer s.mu.Unlock()

	lockPath := s.path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return &stateFile{Version: stateVersion, Sessions: make(map[string]*domain.Session)}
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return &stateFile{Version: stateVersion, Sessions: make(map[string]*domain.Session)}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

func (s *Store) load() *stateFile {
	return s.withSharedFileLock(func() *stateFile {
		sf, err := s.loadLocked()
		if err != nil {
			// Read paths degrade to empty on a corrupted file rather than fail-fast:
			// they have no error return to propagate to (Get/All predate this), so
			// they'd need a signature change to surface it — tracked separately as a
			// later, wider migration. Put/Update/Delete are the load-bearing paths and
			// do fail-fast below, which is what actually prevents clobbering good state.
			return &stateFile{Version: stateVersion, Sessions: make(map[string]*domain.Session)}
		}
		return sf
	})
}

// legacySession is used for backward-compatible deserialization of the old "slack" field.
type legacySession struct {
	domain.Session
	Slack   *legacySlackThread          `json:"slack,omitempty"`
	Effects map[string]*legacyTaskState `json:"effects,omitempty"`
}

type legacySlackThread struct {
	ThreadTS  string `json:"thread_ts"`
	ChannelID string `json:"channel_id"`
}

type legacyTaskState struct {
	contract.TaskState
	EffectID string `json:"effect_id,omitempty"`
}

type legacyStateFile struct {
	Version  int                       `json:"version"`
	Sessions map[string]*legacySession `json:"sessions"`
}

// loadLocked reads and parses state.json. A missing file is a fresh, empty
// state; any other read or parse error is returned rather than silently
// treated as empty — an empty stateFile handed to a write path would
// overwrite good on-disk state with nothing.
func (s *Store) loadLocked() (*stateFile, error) {
	sf := &stateFile{Version: stateVersion, Sessions: make(map[string]*domain.Session)}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return sf, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	// Try loading with legacy format first to migrate old "slack" fields
	var lsf legacyStateFile
	if err := json.Unmarshal(data, &lsf); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}

	for name, ls := range lsf.Sessions {
		session := &ls.Session
		session.Name = name // ensure name is set from map key
		// Migrate old "slack" field to Conversation
		if ls.Slack != nil && session.Conversation == nil {
			session.Conversation = &domain.Conversation{
				Source: "Slack",
				Metadata: map[string]string{
					"thread_ts":  ls.Slack.ThreadTS,
					"channel_id": ls.Slack.ChannelID,
				},
			}
		}
		migrateEffectsToTasks(session, ls.Effects)
		migrateResourceID(session)
		sf.Sessions[name] = session
	}
	normalizeSessionTree(sf.Sessions)

	return sf, nil
}

func normalizeSessionTree(sessions map[string]*domain.Session) {
	for parentName, parent := range sessions {
		if parent == nil {
			continue
		}
		parent.Children = uniqueSessionNames(parent.Children)
		for _, childName := range parent.Children {
			child := sessions[childName]
			if child == nil || child.ParentSession != "" {
				continue
			}
			if childName != parentName && !wouldCreateCycle(sessions, childName, parentName) {
				child.ParentSession = parentName
			}
		}
	}

	for _, session := range sessions {
		if session != nil {
			session.Children = nil
		}
	}

	for childName, child := range sessions {
		if child == nil || child.ParentSession == "" {
			continue
		}
		if rootTarget, ok := strings.CutPrefix(child.ParentSession, "root:"); ok {
			// A "root:<session>" parent is a pseudo-node (that session's own
			// implicit root, domain.ImplicitRootParent) — not a Session in
			// this map, so it has no Children slot to append into. It is
			// valid as long as the named session actually exists.
			if rootTarget == "" || rootTarget == childName || sessions[rootTarget] == nil {
				child.ParentSession = ""
			}
			continue
		}
		parent := sessions[child.ParentSession]
		if parent == nil || child.ParentSession == childName || wouldCreateCycle(sessions, childName, child.ParentSession) {
			child.ParentSession = ""
			continue
		}
		parent.Children = append(parent.Children, childName)
	}

	for _, session := range sessions {
		if session != nil {
			session.Children = uniqueSessionNames(session.Children)
		}
	}
}

func wouldCreateCycle(sessions map[string]*domain.Session, childName, parentName string) bool {
	seen := map[string]bool{}
	for cur := parentName; cur != ""; {
		if cur == childName {
			return true
		}
		if seen[cur] {
			return true
		}
		seen[cur] = true
		parent := sessions[cur]
		if parent == nil {
			return false
		}
		cur = parent.ParentSession
	}
	return false
}

func uniqueSessionNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := names[:0]
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func removeSessionName(names []string, remove string) []string {
	if len(names) == 0 {
		return nil
	}
	out := names[:0]
	for _, name := range names {
		if name != remove {
			out = append(out, name)
		}
	}
	return uniqueSessionNames(out)
}

// migrateResourceID backfills the create-time alias from the canonical
// resource id, which is what a session created without an explicit alias
// was looked up by.
func migrateResourceID(session *domain.Session) {
	if session.Alias == "" && session.ResourceID != "" {
		session.Alias = session.ResourceID
	}
}

func migrateEffectsToTasks(session *domain.Session, effects map[string]*legacyTaskState) {
	if len(effects) == 0 {
		return
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState, len(effects))
	}
	for key, legacy := range effects {
		if legacy == nil {
			continue
		}
		if _, exists := session.Tasks[key]; exists {
			continue
		}
		st := legacy.TaskState
		if st.TaskID == "" {
			st.TaskID = legacy.EffectID
		}
		session.Tasks[key] = &st
	}
}

func (s *Store) saveLocked(sf *stateFile) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := atomicfile.Write(s.path, data); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	return nil
}
