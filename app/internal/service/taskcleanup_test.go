package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// cleanup runs the instance's cleanup script and removes it from state so the
// name is free for a subsequent setup (the dispatcher's recreate idiom).
func TestTaskCleanup_RemovesInstanceAndRunsScript(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "touch " + marker}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("TaskCleanup: %v", err)
	}
	if !res.Found {
		t.Error("Found = false, want true for a present instance")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("cleanup script did not run: %v", err)
	}
	if store.Get("o/r-1").Tasks["initial"] != nil {
		t.Error("instance must be removed from state (frees the name for re-setup)")
	}
}

// Removing the instance lets a same-name setup recreate without colliding —
// this is exactly the dispatcher's `cleanup initial; setup … --name initial`.
func TestTaskCleanup_FreesNameForReSetup(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("re-setup after cleanup must not collide: %v", err)
	}
}

// An absent instance is a no-op success (Found=false), so the dispatcher's
// first-run `cleanup initial` (nothing yet) is harmless.
func TestTaskCleanup_AbsentIsNoOp(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	res, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("cleanup of absent instance must be a no-op, got: %v", err)
	}
	if res.Found {
		t.Error("Found = true, want false for an absent instance")
	}
}

// A concurrent TaskSetup that reserves a new instance during a slow cleanup
// must survive: the cleanup persists only its own key, not a stale whole-session
// snapshot that would clobber the new reservation.
func TestTaskCleanup_DoesNotClobberConcurrentSetup(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	gate := filepath.Join(dir, "go")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "slow", scope: "session", setup: `echo '{}'`,
				cleanup: `touch ` + started + `; while [ ! -e ` + gate + ` ]; do sleep 0.01; done`},
			{id: "fast", scope: "session", setup: `echo '{}'`},
		},
		[]nodeFixture{{id: "slow"}, {id: "fast"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "slow", SessionName: "o/r-1", Name: "victim"}); err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "victim", SessionName: "o/r-1"})
		done <- err
	}()

	// Once the cleanup script is running, reserve a fresh instance concurrently.
	waitUntil(t, func() bool { _, err := os.Stat(started); return err == nil })
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "fast", SessionName: "o/r-1", Name: "fresh"}); err != nil {
		t.Fatalf("concurrent setup: %v", err)
	}

	if err := os.WriteFile(gate, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	s := store.Get("o/r-1")
	if s.Tasks["victim"] != nil {
		t.Error("victim should be reclaimed")
	}
	if s.Tasks["fresh"] == nil {
		t.Error("concurrently-reserved 'fresh' was clobbered by the cleanup persist")
	}
}

// cleanup reclaims by key regardless of which task produced it — a task
// drift that left `initial` on the wrong task is still swept.
func TestTaskCleanup_ReclaimsRegardlessOfTaskID(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"},
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "work"}, {id: "review"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	// `initial` was produced by the wrong task (review).
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"})
	if err != nil || !res.Found {
		t.Fatalf("cleanup initial = (%+v, %v), want Found", res, err)
	}
	if store.Get("o/r-1").Tasks["initial"] != nil {
		t.Error("initial should be reclaimed even though it was a review instance")
	}
}

// When the cleanup script fails, TaskCleanup persists the failed status so
// it's inspectable for retry. If that persist itself fails, the error must
// not be discarded — it's the only sign the failed status never landed.
// The cleanup script corrupts state.json itself before exiting non-zero, so
// the persist attempted right after the (already-successful) session lookup
// is what fails.
func TestTaskCleanup_JoinsPersistFailureWithCleanupFailure(t *testing.T) {
	store := testStore(t)
	statePath := filepath.Join(store.Dir(), "state.json")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`,
			cleanup: "printf '{not valid json' > " + statePath + " && exit 1"}},
		[]nodeFixture{{id: "work"}},
	)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"})
	if err == nil {
		t.Fatal("expected an error: the cleanup script fails and the post-failure persist also fails")
	}
	if !strings.Contains(err.Error(), "state:") {
		t.Errorf("expected the persist failure to be joined into the returned error, got: %s", err.Error())
	}
}

// A partial unwind's record has to survive the failure that produced it: the
// per-layer outcome is what tells the retry which layers are still owed, and
// it lives only in the snapshot the cleanup mutated in place. Without it the
// retry reloads every layer as produced and releases one twice.
func TestTaskCleanup_NestedPartialUnwindSurvivesForTheRetry(t *testing.T) {
	dir := t.TempDir()
	innerLog := filepath.Join(dir, "inner-runs")
	gate := filepath.Join(dir, "outer-may-succeed")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "runtime", scope: "session", setup: `echo '{}'`, cleanup: "echo ran >> " + innerLog},
			{id: "team_runtime", scope: "session", cleanup: "test -f " + gate,
				extra: "inner = \"runtime\"\n"},
		},
		[]nodeFixture{{id: "team_runtime"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "team_runtime", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"}); err == nil {
		t.Fatal("TaskCleanup: want the outer layer's cleanup to fail")
	}

	st := store.Get("o/r-1").Tasks["initial"]
	if st == nil {
		t.Fatal("a failed cleanup must leave the instance inspectable for retry")
	}
	if len(st.Layers) != 2 {
		t.Fatalf("Layers = %+v, want both layer records persisted", st.Layers)
	}
	if st.Layers[1].Status != contract.TaskStatusCleaned {
		t.Errorf("inner layer = %q, want %q — it released successfully", st.Layers[1].Status, contract.TaskStatusCleaned)
	}
	if st.Layers[0].Status != contract.TaskStatusFailed {
		t.Errorf("outer layer = %q, want %q", st.Layers[0].Status, contract.TaskStatusFailed)
	}

	// The operator fixes what made the outer layer fail and retries.
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	runs, err := os.ReadFile(innerLog)
	if err != nil {
		t.Fatalf("read inner cleanup log: %v", err)
	}
	if got := strings.Count(string(runs), "ran"); got != 1 {
		t.Errorf("inner cleanup ran %d times, want exactly once — the retry owes only the layer that failed", got)
	}
	if store.Get("o/r-1").Tasks["initial"] != nil {
		t.Error("a successful retry must reclaim the instance")
	}
}

// TestTaskCleanup_NestedOuterReleasesItsProjectedLocal drives the teardown
// path the way a session's own destroy does: the layer chain is rebuilt from
// the declaration alone, with no compiled plan in scope. An outer layer
// releases what its setup produced as a private local by projecting it into
// its own public contract, so that projection has to survive the rebuild —
// otherwise the release resolves against nothing and the resource leaks.
func TestTaskCleanup_NestedOuterReleasesItsProjectedLocal(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	guard := filepath.Join(dir, "guard")
	released := filepath.Join(dir, "released")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "runtime", scope: "session", setup: `echo '{"pid":"42"}'`},
			{
				id:      "team_runtime",
				scope:   "session",
				setup:   "mkdir -p " + guard + ` && echo '{"guard_dir":"` + guard + `"}'`,
				cleanup: "echo {{.Self.guard_dir}} > " + released,
				extra: "inner = \"runtime\"\n" + `
[outputs.bind]
guard_dir = { from = "locals.guard_dir" }

[locals_schema]
type     = "object"
required = ["guard_dir"]

[locals_schema.properties.guard_dir]
type = "string"

[outputs_schema]
type = "object"

[outputs_schema.properties.guard_dir]
type = "string"
`,
			},
		},
		[]nodeFixture{{id: "team_runtime"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "team_runtime", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"}); err != nil {
		t.Fatalf("TaskCleanup: %v", err)
	}
	data, err := os.ReadFile(released)
	if err != nil {
		t.Fatalf("the outer layer's cleanup never ran: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != guard {
		t.Errorf("released %q, want the local the outer setup produced (%q)", got, guard)
	}
}
