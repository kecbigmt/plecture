package state

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/plecture/plect/app/internal/domain"
)

func TestStore_PutAndGet(t *testing.T) {
	store := NewStore(t.TempDir())

	now := time.Now()
	session := &domain.Session{
		Name:         "owner/repo-123",
		ResourceID:   "https://example.test/owner/repo/items/123",
		Branch:       "issue/123",
		WorktreePath: "/tmp/worktrees/github.com/owner/repo/issue-123",
		Conversation: &domain.Conversation{
			Source: "Slack",
			URL:    "https://exampleorg.slack.com/archives/C01ABCDEF/p1234567890123456",
			Metadata: map[string]string{
				"thread_ts":  "1234567890.123456",
				"channel_id": "C01ABCDEF",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Put(session); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	got := store.Get("owner/repo-123")
	if got == nil {
		t.Fatal("Get() returned nil")
	}

	if got.Name != session.Name {
		t.Errorf("Name = %q, want %q", got.Name, session.Name)
	}
	if got.ResourceID != session.ResourceID {
		t.Errorf("ResourceID = %q, want %q", got.ResourceID, session.ResourceID)
	}
	if got.Conversation == nil || got.Conversation.Source != "Slack" {
		t.Errorf("Conversation not persisted correctly")
	}
	if got.Conversation.URL != "https://exampleorg.slack.com/archives/C01ABCDEF/p1234567890123456" {
		t.Errorf("Conversation URL = %q", got.Conversation.URL)
	}
}

func TestStore_DefaultDirUsesPlectureDataDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_DATA_HOME", "")

	store := NewStore("")
	want := filepath.Join(tmpHome, ".local", "share", "plect")
	if got := store.Dir(); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestStore_GetMissing(t *testing.T) {
	store := NewStore(t.TempDir())

	got := store.Get("nonexistent")
	if got != nil {
		t.Errorf("Get() = %v, want nil", got)
	}
}

func TestStore_Delete(t *testing.T) {
	store := NewStore(t.TempDir())

	session := &domain.Session{
		Name:      "owner/repo-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	store.Put(session)

	if err := store.Delete("owner/repo-1"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if got := store.Get("owner/repo-1"); got != nil {
		t.Error("Get() after Delete() should return nil")
	}
}

func TestStore_NormalizesSessionTree(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()

	if err := store.Put(&domain.Session{Name: "root", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&domain.Session{Name: "work", ParentSession: "root", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&domain.Session{Name: "review", ParentSession: "root", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	root := store.Get("root")
	if root == nil {
		t.Fatal("root missing")
	}
	if got, want := root.Children, []string{"review", "work"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("root.Children = %v, want %v", got, want)
	}
}

func TestStore_NormalizeSessionTreeTreatsRootPrefixAsPseudoParent(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()

	if err := store.Put(&domain.Session{Name: "x", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&domain.Session{Name: "reviewer", ParentSession: "root:x", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	reviewer := store.Get("reviewer")
	if reviewer == nil {
		t.Fatal("reviewer missing")
	}
	if reviewer.ParentSession != "root:x" {
		t.Fatalf("reviewer.ParentSession = %q, want %q (a valid root: pseudo-parent must survive normalization)", reviewer.ParentSession, "root:x")
	}
	// x has no Children slot for the pseudo-parent — reviewer isn't x's child,
	// it's x's sibling (both under root:x); domain.RelationFromTarget derives that.
	if x := store.Get("x"); len(x.Children) != 0 {
		t.Fatalf("x.Children = %v, want empty", x.Children)
	}
}

func TestStore_NormalizeSessionTreeClearsDanglingRootPrefix(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()

	if err := store.Put(&domain.Session{Name: "reviewer", ParentSession: "root:missing", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	reviewer := store.Get("reviewer")
	if reviewer.ParentSession != "" {
		t.Fatalf("reviewer.ParentSession = %q, want empty (root: target does not exist)", reviewer.ParentSession)
	}
}

func TestStore_DeleteDetachesSessionTreeLinks(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()

	if err := store.Put(&domain.Session{Name: "root", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&domain.Session{Name: "work", ParentSession: "root", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&domain.Session{Name: "child", ParentSession: "work", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("work"); err != nil {
		t.Fatal(err)
	}

	root := store.Get("root")
	if root == nil {
		t.Fatal("root missing")
	}
	if len(root.Children) != 0 {
		t.Fatalf("root.Children = %v, want empty after deleting child", root.Children)
	}
	child := store.Get("child")
	if child == nil {
		t.Fatal("child missing")
	}
	if child.ParentSession != "" {
		t.Fatalf("child.ParentSession = %q, want detached", child.ParentSession)
	}
}

func TestStore_All(t *testing.T) {
	store := NewStore(t.TempDir())

	now := time.Now()
	for _, name := range []string{"a/b-1", "c/d-2", "e/f-3"} {
		store.Put(&domain.Session{
			Name:      name,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	all := store.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d sessions, want 3", len(all))
	}
}

func TestStore_MigrateLegacySlack(t *testing.T) {
	dir := t.TempDir()

	// Write old-format state.json with "slack" field
	stateJSON := `{
		"version": 1,
		"sessions": {
			"owner/repo-1": {
				"session_name": "owner/repo-1",
				"url": "https://github.com/owner/repo/issues/1",
				"url_type": "issue",
				"owner_repo": "owner/repo",
				"number": 1,
				"branch": "issue/1",
				"worktree_path": "/tmp/wt",
				"slack": {
					"thread_ts": "9999999999.999999",
					"channel_id": "COLD123"
				}
			}
		}
	}`
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(stateJSON), 0644)

	store := NewStore(dir)
	got := store.Get("owner/repo-1")
	if got == nil {
		t.Fatal("Get() returned nil")
	}

	if got.Conversation == nil {
		t.Fatal("legacy slack field should be migrated to Conversation")
	}
	if got.Conversation.Source != "Slack" {
		t.Errorf("Source = %q, want %q", got.Conversation.Source, "Slack")
	}
	if got.Conversation.Metadata["thread_ts"] != "9999999999.999999" {
		t.Errorf("thread_ts = %q", got.Conversation.Metadata["thread_ts"])
	}
	if got.Conversation.Metadata["channel_id"] != "COLD123" {
		t.Errorf("channel_id = %q", got.Conversation.Metadata["channel_id"])
	}
}

// TestStore_ConcurrentPut verifies that concurrent Put calls from multiple
// Store instances (simulating separate processes sharing the same lock file)
// do not cause lost updates.
func TestStore_ConcurrentPut(t *testing.T) {
	dir := t.TempDir()
	const n = 20

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			// Each goroutine creates its own Store instance (like separate processes)
			store := NewStore(dir)
			name := fmt.Sprintf("owner/repo-%d", i)
			now := time.Now()
			err := store.Put(&domain.Session{
				Name:      name,
				CreatedAt: now,
				UpdatedAt: now,
			})
			if err != nil {
				t.Errorf("Put(%q) error: %v", name, err)
			}
		}(i)
	}
	wg.Wait()

	store := NewStore(dir)
	all := store.All()
	if len(all) != n {
		t.Errorf("expected %d sessions, got %d (lost updates detected)", n, len(all))
	}
}

func TestStore_ConcurrentPutAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	const n = 12

	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestStorePutHelperProcess", "--", dir, strconv.Itoa(i))
			cmd.Env = append(os.Environ(), "PLECT_STATE_PUT_HELPER=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("helper %d: %w: %s", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	store := NewStore(dir)
	all := store.All()
	if len(all) != n {
		t.Fatalf("All() returned %d sessions, want %d", len(all), n)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("owner/repo-process-%d", i)
		if all[name] == nil {
			t.Fatalf("missing session %q after concurrent process writes", name)
		}
	}
}

func TestStorePutHelperProcess(t *testing.T) {
	if os.Getenv("PLECT_STATE_PUT_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: -- <dir> <index>\n")
		os.Exit(2)
	}
	dir := args[1]
	i, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad index: %v\n", err)
		os.Exit(2)
	}
	now := time.Now()
	name := fmt.Sprintf("owner/repo-process-%d", i)
	if err := NewStore(dir).Put(&domain.Session{
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Write with one store instance
	store1 := NewStore(dir)
	store1.Put(&domain.Session{
		Name:      "owner/repo-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	// Read with a new store instance
	store2 := NewStore(dir)
	got := store2.Get("owner/repo-1")
	if got == nil {
		t.Fatal("Session not persisted across store instances")
	}
}

func TestLoad_BackfillsAliasFromResourceID(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	// A session written without an explicit alias was looked up by its
	// resource id, so loading must make that lookup keep working.
	state := `{
  "version": 5,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "resource_id": "https://example.test/org/repo/items/1"
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	s := store.Get("org/repo-1")
	if s == nil {
		t.Fatal("session not loaded")
	}
	if s.Alias != "https://example.test/org/repo/items/1" {
		t.Errorf("Alias = %q, want it backfilled from the resource id", s.Alias)
	}
}

func TestLoad_MigratesEffectsToTasks(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	v3 := `{
  "version": 3,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "url": "https://github.com/org/repo/issues/1",
      "url_type": "issue",
      "owner_repo": "org/repo",
      "number": 1,
      "effects": {
        "review#1": {
          "scope": "session",
          "effect_id": "review",
          "status": "produced",
          "dynamic": true,
          "outputs": {"checks_status": "SUCCESS"}
        }
      }
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(v3), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	s := store.Get("org/repo-1")
	if s == nil {
		t.Fatal("session not loaded")
	}
	task := s.Tasks["review#1"]
	if task == nil {
		t.Fatalf("legacy effects were not migrated: %+v", s.Tasks)
	}
	if task.TaskID != "review" {
		t.Fatalf("TaskID = %q, want review", task.TaskID)
	}

	if err := store.Put(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"] != float64(5) {
		t.Fatalf("version = %v, want 5", raw["version"])
	}
	session := raw["sessions"].(map[string]any)["org/repo-1"].(map[string]any)
	if _, ok := session["effects"]; ok {
		t.Fatal("rewritten state must not contain legacy effects")
	}
	if _, ok := session["tasks"]; !ok {
		t.Fatal("rewritten state must contain tasks")
	}
}

func TestFindByAlias(t *testing.T) {
	store := NewStore(t.TempDir())
	url := "https://github.com/org/repo/issues/9"
	for _, name := range []string{"org/repo-9", "org/repo-9+review"} {
		if err := store.Put(&domain.Session{Name: name, ResourceID: url, Alias: url}); err != nil {
			t.Fatal(err)
		}
	}
	hits := store.FindByAlias(url)
	if len(hits) != 2 {
		t.Fatalf("expected both tag variants, got %d", len(hits))
	}
	if store.FindByAlias("") != nil {
		t.Error("empty alias must never match")
	}
}

func TestStore_CorruptedStateFileFailsWritesInsteadOfOverwriting(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	corrupt := []byte("{not valid json")
	if err := os.WriteFile(statePath, corrupt, 0644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)

	if err := store.Put(&domain.Session{Name: "org/repo-1"}); err == nil {
		t.Fatal("Put() over a corrupted state file must fail, not silently overwrite it")
	}
	if err := store.Update("org/repo-1", func(*domain.Session) error { return nil }); err == nil {
		t.Fatal("Update() over a corrupted state file must fail, not silently overwrite it")
	}
	if err := store.Delete("org/repo-1"); err == nil {
		t.Fatal("Delete() over a corrupted state file must fail, not silently overwrite it")
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(corrupt) {
		t.Fatalf("corrupted state file was rewritten: got %q, want unchanged %q", data, corrupt)
	}
}
