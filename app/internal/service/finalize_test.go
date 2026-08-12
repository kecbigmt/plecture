package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// FinalizeTask must refuse — no cleanup attempted, instance left in place —
// when done_when is not currently satisfied. Finalization must never be the
// thing that forces completion (ADR "goal-as-task" D4).
func TestFinalizeTask_RefusesWhenNotSatisfied(t *testing.T) {
	cfg := checkStatusOnlyConfig(t, 0)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "PENDING"},
		},
	})

	if _, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "o/r-1"}); err == nil {
		t.Fatal("expected an error when done_when is not satisfied")
	}
	if store.Get("o/r-1").Tasks["initial"] == nil {
		t.Error("instance must not be reclaimed when finalize refuses")
	}
}

// A revision approved (or an output persisted as satisfied) before the most
// recent change must not let finalize act on stale data: finalize must refresh
// dynamic outputs first, exactly as tick does, so a resource that has since
// moved on (a new commit, a check going red) is caught before cleanup reclaims
// the instance.
func TestFinalizeTask_RefreshesDynamicOutputsBeforeReconfirming(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(marker, []byte("FAILURE"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{reviewFixtureWithOutput("cat " + marker)},
		[]nodeFixture{{id: "review"}})
	store := testStore(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"initial": {
			TaskID:  "review",
			Dynamic: true,
			Status:  contract.TaskStatusProduced,
			// Stale persisted value: satisfies done_when if read as-is, but the
			// dynamic output script (the "live" source of truth) now says FAILURE.
			Outputs: map[string]any{"checks_status": "SUCCESS"},
		},
	})

	if _, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "owner/repo-1"}); err == nil {
		t.Fatal("expected finalize to refuse once the refreshed output shows FAILURE, not the stale persisted SUCCESS")
	}
	if store.Get("owner/repo-1").Tasks["initial"] == nil {
		t.Error("instance must not be reclaimed")
	}
	if got := store.Get("owner/repo-1").Tasks["initial"].Outputs["checks_status"]; got != "FAILURE" {
		t.Errorf("checks_status = %v, want the refresh to have persisted the live FAILURE value", got)
	}
}

// A dynamic output whose refresh fetch fails outright (script error, network
// blip) is NOT a top-level RefreshInstanceOutputs error — tick/check tolerate
// it and leave the prior persisted value untouched. finalize cannot make that
// trade: a fetch failure means the current-revision reconfirmation the fetch
// was supposed to provide never happened, so finalize must fail closed rather
// than silently evaluate the untouched (possibly stale-satisfied) old value.
func TestFinalizeTask_FailedOutputRefreshFailsClosed(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{reviewFixtureWithOutput("echo boom >&2; exit 1")},
		[]nodeFixture{{id: "review"}})
	store := testStore(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"initial": {
			TaskID:  "review",
			Dynamic: true,
			Status:  contract.TaskStatusProduced,
			// Stale persisted value: satisfies done_when if trusted as-is, but the
			// live refresh attempt below fails outright rather than returning a
			// fresh value.
			Outputs: map[string]any{"checks_status": "SUCCESS"},
		},
	})

	if _, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "owner/repo-1"}); err == nil {
		t.Fatal("expected finalize to fail closed when the refresh fetch itself fails")
	}
	if store.Get("owner/repo-1").Tasks["initial"] == nil {
		t.Error("instance must not be reclaimed when the refresh fetch fails")
	}
}

// Satisfied + no bound resource: finalize records completion and leaves the
// instance in place — cleanup is a separate, explicit step.
func TestFinalizeTask_SatisfiedNoResourceLeavesInstanceForCleanup(t *testing.T) {
	cfg := checkStatusOnlyConfig(t, 0)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS"},
		},
	})

	result, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("FinalizeTask: %v", err)
	}
	if result.Finalized {
		t.Error("Finalized = true, want false with no bound resource")
	}
	st := store.Get("o/r-1").Tasks["initial"]
	if st == nil {
		t.Fatal("instance must not be reclaimed by finalize — cleanup is a separate step")
	}
	if st.FinalizedAt.IsZero() {
		t.Error("FinalizedAt must be recorded")
	}

	cleanup, cerr := TaskCleanup(cfg, store, TaskCleanupParams{Instance: "initial", SessionName: "o/r-1"})
	if cerr != nil {
		t.Fatalf("TaskCleanup after finalize: %v", cerr)
	}
	if !cleanup.Found {
		t.Error("cleanup after finalize must find and reclaim the instance")
	}
	if store.Get("o/r-1").Tasks["initial"] != nil {
		t.Error("instance must be reclaimed after the explicit cleanup call")
	}
}

// Satisfied + a bound resource whose definition declares a finalize script:
// the script runs (seeing the instance/resource/revision as evidence); the
// instance itself is left in place for a separate `plecture task cleanup`.
func TestFinalizeTask_RunsResourceFinalizeAndLeavesInstance(t *testing.T) {
	cfg := checkStatusOnlyConfig(t, 0)
	out := filepath.Join(t.TempDir(), "finalize.out")
	resourcesDir := filepath.Join(cfg.BaseDir, "resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf(`
match    = '^local-okf://'
observe  = "echo '{}'"
finalize = "echo '{{.ResourceID}} {{.Instance}} {{.Revision}}' > %s"
`, out)
	if err := os.WriteFile(filepath.Join(resourcesDir, "local-okf.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "local-okf://kec/goals/x.md",
			Outputs:  map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})

	result, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("FinalizeTask: %v", err)
	}
	if !result.Finalized || result.Definition != "local-okf" {
		t.Fatalf("result = %+v, want Finalized=true via local-okf", result)
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if got := strings.TrimSpace(string(data)); got != "local-okf://kec/goals/x.md initial sha1" {
		t.Errorf("finalize script saw %q, want the resource id, instance, and revision", got)
	}
	st := store.Get("o/r-1").Tasks["initial"]
	if st == nil {
		t.Fatal("instance must not be reclaimed by finalize — cleanup is a separate step")
	}
	if st.FinalizedAt.IsZero() {
		t.Error("FinalizedAt must be recorded")
	}
}

// A resource whose finalize script fails must abort before cleanup — the
// instance is left in place for retry, rather than silently reclaimed with no
// completion record.
func TestFinalizeTask_ResourceFinalizeFailureAbortsBeforeCleanup(t *testing.T) {
	cfg := checkStatusOnlyConfig(t, 0)
	resourcesDir := filepath.Join(cfg.BaseDir, "resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourcesDir, "local-okf.toml"), []byte(`
match    = '^local-okf://'
observe  = "echo '{}'"
finalize = "echo boom >&2; exit 1"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "local-okf://kec/goals/x.md",
			Outputs:  map[string]any{"checks_status": "SUCCESS"},
		},
	})

	if _, err := FinalizeTask(cfg, store, FinalizeTaskParams{Instance: "initial", SessionName: "o/r-1"}); err == nil {
		t.Fatal("expected the finalize script failure to error")
	}
	if store.Get("o/r-1").Tasks["initial"] == nil {
		t.Error("instance must not be reclaimed when finalize fails")
	}
}

func TestFinalizeTask_InstanceRequired(t *testing.T) {
	cfg := checkStatusOnlyConfig(t, 0)
	store := testStore(t)
	if _, err := FinalizeTask(cfg, store, FinalizeTaskParams{SessionName: "o/r-1"}); err == nil {
		t.Fatal("expected an error for an empty instance")
	}
}
