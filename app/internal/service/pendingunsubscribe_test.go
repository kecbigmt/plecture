package service

import (
	"os"
	"path/filepath"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// A TaskCleanup whose unsubscribe hook fails queues the resource durably —
// the instance record that would otherwise be the retry handle is already
// gone by the time this runs, so the queue is the only surviving trace of
// the failure.
func TestTaskCleanup_UnsubscribeFailureIsDurablyQueued(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "work"}},
	)
	body := `
[github]
kind  = "workspace_provider"
match = '` + ghMatch + `'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[github.setup]
type    = "exec"
command = "printf"
args    = ['{"workdir":"/tmp/x"}']

[github.subscribe]
type    = "exec"
command = "true"

[github.unsubscribe]
type    = "exec"
command = "sh"
args    = ["-c", "exit 3"]
`
	if err := os.MkdirAll(filepath.Join(cfg.BaseDir, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "workspaces", "github.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	const prURL = "https://github.com/o/r/pull/9"
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "pr", Resource: prURL}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "pr", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if result.Unsubscribed || result.UnsubscribeError == "" {
		t.Fatalf("result = %+v, want a failed-but-non-fatal unsubscribe", result)
	}

	f := loadPendingUnsubscribe(pendingUnsubscribePath(store))
	if got := f.Resources["o/r-1"]; len(got) != 1 || got[0] != prURL {
		t.Fatalf("pending queue = %v, want [%s] for o/r-1", got, prURL)
	}
}

// toggledUnsubscribeProvider's unsubscribe hook fails until toggle exists,
// then succeeds and records its call to rec — so a test can flip a real
// hook from "always fails" to "works" between a queuing attempt and a
// later retry.
func toggledUnsubscribeProvider(t *testing.T, baseDir, toggle, rec string) {
	t.Helper()
	body := `
[github]
kind  = "workspace_provider"
match = '` + ghMatch + `'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[github.setup]
type    = "exec"
command = "printf"
args    = ['{"workdir":"/tmp/x"}']

[github.subscribe]
type    = "exec"
command = "true"

[github.unsubscribe]
type    = "exec"
command = "sh"
args    = ["-c", 'test -e "$1" || exit 3; echo done > "$2"', "provider", "` + toggle + `", "` + rec + `"]
`
	if err := os.MkdirAll(filepath.Join(baseDir, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces", "github.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The queue drains on the session's next activity, once the hook starts
// working — this is the durable-retry path TaskCleanup's own failure alone
// cannot provide, since its instance record (the natural retry handle) is
// already gone by the time the hook fails.
func TestFlushPendingUnsubscribes_RetriesAndDrainsOnSuccess(t *testing.T) {
	toggle := filepath.Join(t.TempDir(), "toggle")
	rec := filepath.Join(t.TempDir(), "rec")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "work"}},
	)
	toggledUnsubscribeProvider(t, cfg.BaseDir, toggle, rec)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	const prURL = "https://github.com/o/r/pull/9"
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "pr", Resource: prURL}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "pr", SessionName: "o/r-1"}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if f := loadPendingUnsubscribe(pendingUnsubscribePath(store)); len(f.Resources["o/r-1"]) != 1 {
		t.Fatalf("precondition: expected %s queued, got %v", prURL, f.Resources)
	}

	if err := os.WriteFile(toggle, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	flushPendingUnsubscribes(cfg, store, "o/r-1")

	if _, err := os.Stat(rec); err != nil {
		t.Errorf("the retried unsubscribe hook did not run: %v", err)
	}
	if f := loadPendingUnsubscribe(pendingUnsubscribePath(store)); len(f.Resources["o/r-1"]) != 0 {
		t.Errorf("pending queue = %v, want empty after a successful retry", f.Resources)
	}
}

// A resource re-bound by another instance since the original failure is
// dropped from the queue without running the hook: whatever queued it is
// moot once something else has since claimed the resource again.
func TestFlushPendingUnsubscribes_DropsEntryOnceResourceIsNeededAgain(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "work"}},
	)
	// A toggle that never appears: if the hook ran at all, the test fails.
	toggledUnsubscribeProvider(t, cfg.BaseDir, filepath.Join(t.TempDir(), "never"), rec)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	const prURL = "https://github.com/o/r/pull/9"
	// Bind the resource to a live instance BEFORE queuing: TaskSetup flushes
	// the queue itself at its own start, so queuing first would let that
	// internal flush (running before "again" exists) drop the entry for the
	// wrong reason.
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "again", Resource: prURL}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	queuePendingUnsubscribe(store, "o/r-1", prURL)

	flushPendingUnsubscribes(cfg, store, "o/r-1")

	if _, err := os.Stat(rec); err == nil {
		t.Error("a resource needed again must not have its unsubscribe hook run")
	}
	if f := loadPendingUnsubscribe(pendingUnsubscribePath(store)); len(f.Resources["o/r-1"]) != 0 {
		t.Errorf("pending queue = %v, want the now-needed entry dropped", f.Resources)
	}
}
