package state

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestStore_PutAndGet(t *testing.T) {
	store := NewStore(t.TempDir())

	now := time.Now()
	session := &domain.Session{
		Name:             "owner/repo-123",
		ResourceID:       "https://example.test/owner/repo/items/123",
		Branch:           "issue/123",
		WorkspaceDirPath: "/tmp/workdirs/github.com/owner/repo/issue-123",
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

func TestStore_CheckReadableAllowsMissingStateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	store := NewStore(dir)

	if err := store.CheckReadable(); err != nil {
		t.Fatalf("CheckReadable() with no state file: %v", err)
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

// Each attempt uses its own Store instance, like TestStore_ConcurrentPut, so
// this exercises the real cross-process file lock rather than only the
// in-process mutex. Each attempt reserves a distinctly-named child, since
// reservations are keyed by child, not by an anonymous per-parent count.
func TestStore_ReserveUpSlotSerializesConcurrentReservations(t *testing.T) {
	dir := t.TempDir()
	const limit = 3
	const attempts = 20

	var wg sync.WaitGroup
	results := make([]bool, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			store := NewStore(dir)
			child := fmt.Sprintf("child%d", i)
			approved, err := store.ReserveUpSlot(child, "parent1", func(sessions map[string]*domain.Session, reservations map[string]UpReservation) bool {
				return len(reservations) < limit
			})
			if err != nil {
				t.Errorf("ReserveUpSlot: %v", err)
				return
			}
			results[i] = approved
		}(i)
	}
	wg.Wait()

	approved := 0
	for _, ok := range results {
		if ok {
			approved++
		}
	}
	if approved != limit {
		t.Fatalf("approved = %d, want exactly %d (the declared limit) despite %d concurrent attempts", approved, limit, attempts)
	}

	sf, err := NewStore(dir).loadE()
	if err != nil {
		t.Fatalf("loadE: %v", err)
	}
	if len(sf.UpReservations) != limit {
		t.Errorf("len(UpReservations) = %d, want %d", len(sf.UpReservations), limit)
	}
}

func TestStore_ReleaseUpSlotDropsTheNamedReservation(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, child := range []string{"childA", "childB"} {
		approved, err := store.ReserveUpSlot(child, "parent1", func(map[string]*domain.Session, map[string]UpReservation) bool { return true })
		if err != nil || !approved {
			t.Fatalf("ReserveUpSlot(%q): approved=%v err=%v", child, approved, err)
		}
	}

	if err := store.ReleaseUpSlot("childA"); err != nil {
		t.Fatalf("ReleaseUpSlot: %v", err)
	}
	sf, err := store.loadE()
	if err != nil {
		t.Fatalf("loadE: %v", err)
	}
	if _, ok := sf.UpReservations["childA"]; ok {
		t.Error("childA's reservation should be gone after ReleaseUpSlot")
	}
	if _, ok := sf.UpReservations["childB"]; !ok {
		t.Error("childB's reservation should survive releasing childA's")
	}
}

func TestStore_ReleaseUpSlotOnUnreservedChildIsANoop(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.ReleaseUpSlot("childA"); err != nil {
		t.Fatalf("ReleaseUpSlot on a child with no reservation: %v", err)
	}
}

// A `plect up` process killed between ReserveUpSlot and its deferred
// ReleaseUpSlot (a SIGKILL, a crashed machine) leaves a reservation
// nobody will ever release. ReserveUpSlot must not let that permanently
// cost its parent a slot — detected by the holder process being gone, not
// by elapsed time (see TestStore_ReserveUpSlotDoesNotExpireALiveReservation
// for why elapsed time alone would be wrong).
func TestStore_ReserveUpSlotExcludesReservationsFromDeadProcesses(t *testing.T) {
	store := NewStore(t.TempDir())
	plantReservation(t, store, "crashed-child", UpReservation{Parent: "parent1", At: time.Now(), PID: deadPID(t)})

	var seen map[string]UpReservation
	approved, err := store.ReserveUpSlot("new-child", "parent1", func(_ map[string]*domain.Session, reservations map[string]UpReservation) bool {
		seen = reservations
		return true
	})
	if err != nil || !approved {
		t.Fatalf("ReserveUpSlot: approved=%v err=%v", approved, err)
	}
	if _, ok := seen["crashed-child"]; ok {
		t.Error("a reservation from a dead process was still visible to the admission decision")
	}
}

// A `plect up` whose RunSetup runs long (an agent session, a slow
// workspace provider) is not the same as a crashed one: its reservation
// must survive as long as its process does, however long that takes —
// this is the case the prior fixed-TTL design got wrong.
func TestStore_ReserveUpSlotDoesNotExpireALiveReservation(t *testing.T) {
	store := NewStore(t.TempDir())
	plantReservation(t, store, "long-running-child", UpReservation{
		Parent: "parent1",
		At:     time.Now().Add(-2 * time.Hour), // longer than the old fixed 30m TTL
		PID:    os.Getpid(),                    // this test process: definitely still alive
	})

	var seen map[string]UpReservation
	approved, err := store.ReserveUpSlot("new-child", "parent1", func(_ map[string]*domain.Session, reservations map[string]UpReservation) bool {
		seen = reservations
		return true
	})
	if err != nil || !approved {
		t.Fatalf("ReserveUpSlot: approved=%v err=%v", approved, err)
	}
	if _, ok := seen["long-running-child"]; !ok {
		t.Error("a reservation held by a still-live process was treated as abandoned")
	}
}

// upReservationBackstopTTL exists only for PID reuse (a crashed holder's
// PID reassigned to an unrelated live process) — a case liveness alone
// can't distinguish from the original holder still running. It applies
// even to a PID that is, coincidentally, this test process's own.
func TestStore_ReserveUpSlotBackstopAppliesEvenToALivePID(t *testing.T) {
	store := NewStore(t.TempDir())
	plantReservation(t, store, "very-old-child", UpReservation{
		Parent: "parent1",
		At:     time.Now().Add(-upReservationBackstopTTL - time.Hour),
		PID:    os.Getpid(),
	})

	var seen map[string]UpReservation
	approved, err := store.ReserveUpSlot("new-child", "parent1", func(_ map[string]*domain.Session, reservations map[string]UpReservation) bool {
		seen = reservations
		return true
	})
	if err != nil || !approved {
		t.Fatalf("ReserveUpSlot: approved=%v err=%v", approved, err)
	}
	if _, ok := seen["very-old-child"]; ok {
		t.Error("the PID-reuse backstop should have excluded a reservation past its TTL")
	}
}

// deadPID returns a PID guaranteed not to be running: a helper process's,
// after it has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("could not run a helper process to obtain a dead PID: %v", err)
	}
	return cmd.Process.Pid
}

// A retried `plect up` for the same child (after a crash, or simply a
// re-run) must reclaim its own reservation immediately rather than waiting
// out the TTL or being blocked by its own leftover entry.
func TestStore_ReserveUpSlotSupersedesItsOwnPriorReservation(t *testing.T) {
	store := NewStore(t.TempDir())
	plantReservation(t, store, "childA", UpReservation{Parent: "parent1", At: time.Now()})

	sawItself := false
	// ReserveUpSlot records the new reservation into this same map right
	// after fn returns, so the assertion must happen inside fn — checking
	// the map afterward would see the write this attempt itself just made.
	approved, err := store.ReserveUpSlot("childA", "parent1", func(_ map[string]*domain.Session, reservations map[string]UpReservation) bool {
		_, sawItself = reservations["childA"]
		return true
	})
	if err != nil || !approved {
		t.Fatalf("ReserveUpSlot: approved=%v err=%v", approved, err)
	}
	if sawItself {
		t.Error("a reservation attempt saw its own prior (fresh, non-expired) reservation as if it were a sibling's")
	}
}

// Destroying a child is the operator-driven recovery path for a stuck
// reservation: it should not have to wait for upReservationTTL to elapse.
func TestStore_DeleteClearsTheSessionsUpReservation(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "childA", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	plantReservation(t, store, "childA", UpReservation{Parent: "parent1", At: now})

	if err := store.Delete("childA"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	sf, err := store.loadE()
	if err != nil {
		t.Fatalf("loadE: %v", err)
	}
	if _, ok := sf.UpReservations["childA"]; ok {
		t.Error("Delete should have cleared childA's reservation")
	}
}

// plantReservation writes res directly into state.json, simulating a
// reservation ReserveUpSlot made in the past (a crashed process's, or one
// old enough to test TTL expiry against) without going through the timing
// ReserveUpSlot itself would stamp.
func plantReservation(t *testing.T, store *Store, child string, res UpReservation) {
	t.Helper()
	if err := store.withFileLock(func() error {
		sf, err := store.loadLocked()
		if err != nil {
			return err
		}
		if sf.UpReservations == nil {
			sf.UpReservations = make(map[string]UpReservation)
		}
		sf.UpReservations[child] = res
		return store.saveLocked(sf)
	}); err != nil {
		t.Fatalf("plantReservation: %v", err)
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
  "version": 7,
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

func TestStore_StateVersionMismatchFailsWritesInsteadOfOverwriting(t *testing.T) {
	tests := []struct {
		name      string
		version   int
		wantParts []string
	}{
		{
			name:    "older",
			version: 6,
			wantParts: []string{
				"state schema version mismatch",
				"got 6",
				"want 7",
				"go run ./plugins/legacy-migration/cmd/legacy-migration",
			},
		},
		{
			name:    "newer",
			version: 8,
			wantParts: []string{
				"state schema version mismatch",
				"got 8",
				"want 7",
				"newer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state.json")
			original := []byte(fmt.Sprintf(`{
  "version": %d,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "workspace_dir_path": "/tmp/workdir"
    }
  }
}`, tt.version))
			if err := os.WriteFile(statePath, original, 0644); err != nil {
				t.Fatal(err)
			}
			store := NewStore(dir)

			if err := store.CheckReadable(); err == nil {
				t.Fatal("CheckReadable() over a mismatched state version must fail")
			}
			if _, err := store.AllE(); err == nil {
				t.Fatal("AllE() over a mismatched state version must fail")
			}
			if _, err := store.GetE("org/repo-1"); err == nil {
				t.Fatal("GetE() over a mismatched state version must fail")
			}
			if _, err := store.FindByAliasE("https://example.test/org/repo/items/1"); err == nil {
				t.Fatal("FindByAliasE() over a mismatched state version must fail")
			}

			err := store.Put(&domain.Session{Name: "org/repo-2"})
			if err == nil {
				t.Fatal("Put() over a mismatched state version must fail, not silently overwrite it")
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), part)
				}
			}

			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != string(original) {
				t.Fatalf("mismatched state file was rewritten: got %q, want unchanged %q", data, original)
			}
		})
	}
}

func TestStore_UnmigratedLayerTaskIDFailsLoudInsteadOfZeroingEffectID(t *testing.T) {
	tests := []struct {
		name     string
		layerRaw string
	}{
		{
			name:     "pre-rename task_id field still present",
			layerRaw: `{"task_id": "some-effect", "status": "produced"}`,
		},
		{
			name:     "effect_id present but empty",
			layerRaw: `{"effect_id": "", "status": "produced"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state.json")
			original := []byte(fmt.Sprintf(`{
  "version": %d,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "workspace_dir_path": "/tmp/workdir",
      "tasks": {
        "nested-task": {
          "scope": "session",
          "status": "produced",
          "layers": [%s]
        }
      }
    }
  }
}`, contract.SchemaVersion, tt.layerRaw))
			if err := os.WriteFile(statePath, original, 0644); err != nil {
				t.Fatal(err)
			}
			store := NewStore(dir)

			if err := store.CheckReadable(); err == nil {
				t.Fatal("CheckReadable() over a pre-rename layers[].task_id record must fail loud, not load-then-zero effect_id")
			} else if !strings.Contains(err.Error(), "effect_id") || !strings.Contains(err.Error(), "task-layer-effect-id-migration.md") {
				t.Fatalf("error = %q, want it to name effect_id and the migration doc", err.Error())
			}

			if _, err := store.AllE(); err == nil {
				t.Fatal("AllE() over a pre-rename layer record must fail loud")
			}

			if err := store.Put(&domain.Session{Name: "org/repo-2"}); err == nil {
				t.Fatal("Put() over a pre-rename layer record must fail loud, not silently write effect_id=\"\"")
			}

			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != string(original) {
				t.Fatalf("pre-rename state file was rewritten: got %q, want unchanged %q", data, original)
			}
		})
	}
}
