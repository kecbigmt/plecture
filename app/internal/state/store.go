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

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/contracts/atomicfile"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const stateVersion = contract.SchemaVersion

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
// If dir is empty, defaults to ~/.local/share/plect.
func NewStore(dir string) *Store {
	if dir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			home, _ := os.UserHomeDir()
			dataHome = filepath.Join(home, ".local", "share")
		}
		dir = filepath.Join(dataHome, "plect")
	}
	return &Store{path: filepath.Join(dir, "state.json")}
}

// Dir returns the directory holding state.json. Co-located stores (e.g. the
// event log) derive their root from it so they share the same data home.
func (s *Store) Dir() string {
	return filepath.Dir(s.path)
}

// CheckReadable verifies that the state file can be loaded by this binary.
func (s *Store) CheckReadable() error {
	_, err := s.loadE()
	return err
}

// Get returns a session by name, or nil if not found.
func (s *Store) Get(name string) *domain.Session {
	sf := s.load()
	return sf.Sessions[name]
}

// GetE returns a session by name while preserving state load errors.
func (s *Store) GetE(name string) (*domain.Session, error) {
	sf, err := s.loadE()
	if err != nil {
		return nil, err
	}
	return sf.Sessions[name], nil
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
// that may race with other plect processes (e.g. a watcher daemon merging
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
	return findByAlias(sf.Sessions, alias)
}

// FindByAliasE returns alias matches while preserving state load errors.
func (s *Store) FindByAliasE(alias string) ([]*domain.Session, error) {
	sf, err := s.loadE()
	if err != nil {
		return nil, err
	}
	return findByAlias(sf.Sessions, alias), nil
}

// All returns all sessions.
func (s *Store) All() map[string]*domain.Session {
	sf := s.load()
	return copySessions(sf.Sessions)
}

// AllE returns all sessions while preserving state load errors.
func (s *Store) AllE() (map[string]*domain.Session, error) {
	sf, err := s.loadE()
	if err != nil {
		return nil, err
	}
	return copySessions(sf.Sessions), nil
}

func findByAlias(sessions map[string]*domain.Session, alias string) []*domain.Session {
	var result []*domain.Session
	for _, session := range sessions {
		if session.Alias != "" && session.Alias == alias {
			result = append(result, session)
		}
	}
	return result
}

func copySessions(sessions map[string]*domain.Session) map[string]*domain.Session {
	result := make(map[string]*domain.Session, len(sessions))
	for k, v := range sessions {
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

func (s *Store) withSharedLockErr(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lockPath := s.path + ".lock"
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("open state lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}

func (s *Store) load() *stateFile {
	return s.withSharedFileLock(func() *stateFile {
		sf, err := s.loadLocked()
		if err != nil {
			// Compatibility read paths degrade to empty when callers have no
			// error return to propagate. New read paths should use loadE.
			return &stateFile{Version: stateVersion, Sessions: make(map[string]*domain.Session)}
		}
		return sf
	})
}

func (s *Store) loadE() (*stateFile, error) {
	var sf *stateFile
	err := s.withSharedLockErr(func() error {
		var err error
		sf, err = s.loadLocked()
		return err
	})
	if err != nil {
		return nil, err
	}
	return sf, nil
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

	if err := checkLayerIdentityMigrated(data); err != nil {
		return nil, err
	}

	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}
	if err := validateStateVersion(header.Version); err != nil {
		return nil, err
	}

	var parsed stateFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}
	if parsed.Sessions == nil {
		parsed.Sessions = make(map[string]*domain.Session)
	}
	if err := validateStateVersion(parsed.Version); err != nil {
		return nil, err
	}

	for name, session := range parsed.Sessions {
		if session == nil {
			continue
		}
		session.Name = name // ensure name is set from map key
		migrateResourceID(session)
		sf.Sessions[name] = session
	}
	normalizeSessionTree(sf.Sessions)

	return sf, nil
}

// checkLayerIdentityMigrated rejects a state file carrying a nesting layer
// with no valid effect_id: either the pre-#270 layers[].task_id field is
// still present, or effect_id itself decoded empty. json.Unmarshal silently
// drops unknown struct fields, so without this check an unmigrated task_id
// would decode as a zero-value EffectID and a later whole-file save would
// persist that zero value, permanently destroying the layer's identity. See
// docs/migrations/task-layer-effect-id-migration.md.
func checkLayerIdentityMigrated(data []byte) error {
	var raw struct {
		Sessions map[string]struct {
			Tasks map[string]struct {
				Layers []map[string]json.RawMessage `json:"layers"`
			} `json:"tasks"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Malformed JSON is reported by the caller's own parse step.
		return nil
	}

	for sessionName, session := range raw.Sessions {
		for taskID, task := range session.Tasks {
			for i, layer := range task.Layers {
				if _, hasLegacyField := layer["task_id"]; hasLegacyField {
					return fmt.Errorf("state file: session %q task %q layer %d still has the pre-rename task_id field instead of effect_id; run the migration in docs/migrations/task-layer-effect-id-migration.md before using this binary", sessionName, taskID, i)
				}
				var effectID string
				if effectIDRaw, ok := layer["effect_id"]; ok {
					_ = json.Unmarshal(effectIDRaw, &effectID)
				}
				if effectID == "" {
					return fmt.Errorf("state file: session %q task %q layer %d has no effect_id; run the migration in docs/migrations/task-layer-effect-id-migration.md before using this binary", sessionName, taskID, i)
				}
			}
		}
	}
	return nil
}

func validateStateVersion(got int) error {
	if got == stateVersion {
		return nil
	}
	if got > stateVersion {
		return fmt.Errorf("state schema version mismatch: got %d, want %d; this state was written by a newer plect binary, so use a matching binary or migrate explicitly", got, stateVersion)
	}
	return fmt.Errorf("state schema version mismatch: got %d, want %d; run `go run ./plugins/legacy-migration/cmd/legacy-migration` before using this plect binary", got, stateVersion)
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
