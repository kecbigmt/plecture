package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// TestDeliveryLock_ConcurrentSetupWaitsOutACleanupsUnsubscribe pins the
// close (not just narrow) of the race a fresh state read alone cannot
// close: TaskCleanup deciding "not needed" and then TaskSetup binding a new
// instance to the same resource before the unsubscribe hook actually runs.
// The unsubscribe hook is deliberately slow, so a concurrent TaskSetup for
// the same resource is guaranteed to still be waiting on the delivery lock
// when it starts; if TaskSetup's own subscribe call could run at the same
// time, it would race the unsubscribe hook to the workspace provider's
// registry and could lose. With the lock, no such race is possible: the
// concurrent TaskSetup's subscribe always runs strictly after the
// unsubscribe hook completes, so the final registry state is always
// "subscribed" — never coin-flipped.
func TestDeliveryLock_ConcurrentSetupWaitsOutACleanupsUnsubscribe(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "unsubscribe-started")
	state := filepath.Join(dir, "registry-state")
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
command = "sh"
args    = ["-c", 'printf subscribed > "$1"', "provider", "` + state + `"]

[github.unsubscribe]
type    = "exec"
command = "sh"
args    = ["-c", 'touch "$1"; sleep 0.3; printf unsubscribed > "$2"', "provider", "` + started + `", "` + state + `"]
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
	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "a", Resource: prURL}); err != nil {
		t.Fatalf("setup a: %v", err)
	}

	cleanupDone := make(chan error, 1)
	go func() {
		_, err := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "a", SessionName: "o/r-1"})
		cleanupDone <- err
	}()

	// Wait until TaskCleanup is inside its unsubscribe hook (holding the
	// delivery lock and about to sleep) before starting the concurrent
	// TaskSetup — otherwise a b that just happens to run after cleanup
	// finishes entirely would prove nothing about the lock.
	waitUntil(t, func() bool { _, err := os.Stat(started); return err == nil })

	setupStart := time.Now()
	setupResult, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "b", Resource: prURL})
	setupElapsed := time.Since(setupStart)
	if err != nil {
		t.Fatalf("setup b: %v", err)
	}
	if !setupResult.Subscribed {
		t.Fatalf("setup b did not subscribe (SubscribeError=%q)", setupResult.SubscribeError)
	}
	// The unsubscribe hook sleeps 300ms; if b's own subscribe call had to
	// wait out the delivery lock, this call cannot have returned in much
	// less than that.
	if setupElapsed < 250*time.Millisecond {
		t.Errorf("setup b returned in %s, want it to have waited out the delivery lock (~300ms)", setupElapsed)
	}

	if err := <-cleanupDone; err != nil {
		t.Fatalf("cleanup a: %v", err)
	}

	got, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "subscribed" {
		t.Errorf("final registry state = %q, want %q — b's subscribe must always run after a's unsubscribe completes, never race it", got, "subscribed")
	}
}
