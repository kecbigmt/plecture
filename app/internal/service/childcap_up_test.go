package service

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// capResolverFields is a resolver fixture private to this file. It
// deliberately avoids dispatch_test.go's shared githubResolverFields: that
// derives a session name shaped like a hosting provider's owner+repo slug,
// which scripts/check-provider-boundary.sh treats as a leaked convention,
// and this file is not on that script's (shrinking) test allowlist.
const capResolverFields = `match = '^https://example\.test/cases/(?P<id>[A-Za-z0-9]+)'
name  = { expr = "'case_' + match.id" }
`

// capProviderCreatingWorkspace mirrors dispatch_test.go's
// providerCreatingWorkspace, but built on capResolverFields instead of the
// shared githubResolverFields.
func capProviderCreatingWorkspace(id, workspaceDir string) string {
	return capProviderRunning(id, fmt.Sprintf("mkdir -p %s\nprintf '{\"workspace_dir\":\"%s\"}'\n", workspaceDir, workspaceDir))
}

// capProviderSlowWorkspace is capProviderCreatingWorkspace but pauses
// first, widening the window a concurrent Up() attempt for the same child
// has to land its own reservation attempt while this one is still in flight.
func capProviderSlowWorkspace(id, workspaceDir string) string {
	return capProviderRunning(id, fmt.Sprintf("sleep 0.5\nmkdir -p %s\nprintf '{\"workspace_dir\":\"%s\"}'\n", workspaceDir, workspaceDir))
}

func capProviderRunning(id, script string) string {
	return providerDoc(id, capResolverFields, `[%[1]s.setup]
type    = "exec"
command = "sh"
args    = ["-c", `+fmt.Sprintf("%q", script)+`, "provider"]
`)
}

// Given a parent whose workflow declares max_up_children = 2 with 2 children
// up, when `plect up` would add a third child, then the command fails
// naming the parent, cap, and current count, and no session/state entry is
// created.
func TestUp_RejectsThirdChildAtCapAndCreatesNoStateEntry(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i, name := range []string{"childA", "childB"} {
		seedSession(t, store, name, "acct", i, "", upTasks())
		setParent(t, store, name, "parent1")
	}

	url := "https://example.test/cases/reject"
	newChildName := "case_reject+capwf"
	_, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "parent1"})
	if err == nil {
		t.Fatal("expected Up to reject the third child")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrChildCapExceeded {
		t.Fatalf("want ErrChildCapExceeded, got %v", err)
	}
	if store.Get(newChildName) != nil {
		t.Fatalf("session %q was persisted despite the cap rejection", newChildName)
	}
}

// Given the same parent after one child is downed or destroyed, when
// `plect up` adds a new child, then it succeeds.
func TestUp_AllowsNewChildAfterSiblingDrops(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSession(t, store, "childA", "acct", 0, "", upTasks())
	setParent(t, store, "childA", "parent1")
	// childB is down (destroyed would remove the entry outright; downed is
	// the case with a state entry but no produced run-scoped task).
	seedSession(t, store, "childB", "acct", 1, "", nil)
	setParent(t, store, "childB", "parent1")

	url := "https://example.test/cases/accept"
	result, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "parent1"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.Get(result.SessionName)
	if s == nil {
		t.Fatal("new child session not persisted")
	}
	if s.ParentSession != "parent1" {
		t.Errorf("ParentSession = %q, want parent1", s.ParentSession)
	}
}

// Given a workflow with no cap declared, when any number of children are
// brought up, then behavior is unchanged from today.
func TestUp_NoCapDeclaredAllowsUnlimitedChildrenViaUp(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", nil)

	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		seedSession(t, store, "child"+name, "acct", i, "", upTasks())
		setParent(t, store, "child"+name, "parent1")
	}

	url := "https://example.test/cases/many"
	if _, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "parent1"}); err != nil {
		t.Fatalf("Up: %v, want success (no cap declared)", err)
	}
}

// Given an already-up child of a capped, full parent, when `plect up` is
// re-run on that child, then it succeeds (idempotency preserved).
func TestUp_ReRunOnAlreadyUpChildAtFullCapStaysIdempotent(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "child_wf",
		[]taskFixture{{id: "noop", scope: "run", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSession(t, store, "childA", "acct", 0, "child_wf", map[string]*contract.TaskState{
		"noop": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "childA", "parent1")
	seedSession(t, store, "childB", "acct", 1, "child_wf", upTasks())
	setParent(t, store, "childB", "parent1")

	if _, err := Up(cfg, store, UpParams{Identifier: "childA"}); err != nil {
		t.Fatalf("Up (re-up on already-up child): %v, want success", err)
	}
}

// A completed Up must release the reservation it took, or every successful
// admission would permanently eat into the cap on top of the real up child
// it produced — double-counting the same slot forever.
func TestUp_ReleasesReservationAfterSuccessfulAdmission(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)

	if _, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/first", ParentSession: "parent1"}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Only one of the two slots is genuinely up now. A second reservation
	// must succeed — it would wrongly fail if the first Up's reservation
	// were still outstanding on top of the real up child it produced.
	reserved, capErr := reserveChildCapSlot(cfg, store, "second", "parent1", false)
	if capErr != nil {
		t.Fatalf("reserveChildCapSlot after a completed Up: %v, want nil", capErr)
	}
	if !reserved {
		t.Error("reserved = false, want true: the first Up's reservation should have been released")
	}
}

// The end-to-end recovery path for a reservation a crashed `plect up` left
// behind: `plect destroy` on the stuck child, an existing command, needs no
// new verb to free the slot for a sibling.
func TestDestroy_ClearsStaleReservationForTheDestroyedChild(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "child_wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSession(t, store, "childA", "acct", 0, "child_wf", nil)
	setParent(t, store, "childA", "parent1")

	if _, err := store.ReserveUpSlot("childA", "parent1", approveAnyReservation); err != nil {
		t.Fatalf("simulate a crashed prior reservation: %v", err)
	}

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "childA", Force: true}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	reserved, capErr := reserveChildCapSlot(cfg, store, "childB", "parent1", false)
	if capErr != nil || !reserved {
		t.Fatalf("childB after destroying childA: reserved=%v err=%v, want true/nil", reserved, capErr)
	}
}

// End-to-end through the real Up() admission path (not just
// reserveChildCapSlot/ReserveUpSlot in isolation): a child reservation held
// by a live process must keep blocking a sibling no matter how long that
// process has held it — see
// TestStore_ReserveUpSlotNeverExpiresALiveReservationRegardlessOfAge for the
// state-layer proof that no amount of elapsed time overrides this.
func TestUp_LiveReservationBlocksSiblingThroughTheRealUpPath(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)

	// childA's `plect up` is still mid-RunSetup: its reservation is
	// genuinely outstanding, held by this (live) test process.
	if _, err := store.ReserveUpSlot("childA", "parent1", approveAnyReservation); err != nil {
		t.Fatalf("simulate an in-progress reservation: %v", err)
	}

	_, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/blocked", ParentSession: "parent1"})
	if err == nil {
		t.Fatal("expected Up to reject a sibling while childA's live reservation stands")
	}
	if svcErr, ok := err.(*Error); !ok || svcErr.Code != ErrChildCapExceeded {
		t.Fatalf("want ErrChildCapExceeded, got %v", err)
	}
}

// End-to-end: real concurrent Up() calls racing to create the exact same
// new child (a doubled-up orchestrator dispatch, say). The cap here is set
// generously — this isn't about max_up_children, it's the same child
// racing itself — so exactly one attempt may win the reservation and the
// rest see it already in progress, never both ending up holding it (see
// TestReserveChildCapSlot_ConcurrentAttemptForTheSameChildIsRejected for
// the isolated proof this exercises through the real Up() path).
func TestUp_ConcurrentSameChildAttemptsRejectAllButOne(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderSlowWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(5))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)

	const attempts = 5
	var wg sync.WaitGroup
	results := make([]error, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/race", ParentSession: "parent1"})
			results[i] = err
		}(i)
	}
	wg.Wait()

	succeeded, inProgress := 0, 0
	for _, err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if svcErr, ok := err.(*Error); ok && svcErr.Code == ErrChildUpInProgress {
			inProgress++
			continue
		}
		t.Errorf("unexpected error from a racing Up(): %v", err)
	}
	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1 of %d concurrent attempts", succeeded, attempts)
	}
	if inProgress != attempts-1 {
		t.Errorf("inProgress = %d, want %d", inProgress, attempts-1)
	}
}
