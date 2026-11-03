package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
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

func TestUp_AcceptsThirdChildAtCapAsQueuedSleep(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i, name := range []string{"childA", "childB"} {
		seedSessionWithNodes(t, store, name, "acct", i, "", upTasks())
		setParent(t, store, name, "parent1")
	}

	url := "https://example.test/cases/reject"
	newChildName := "case_reject+capwf"
	result, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "parent1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "sleeping" || result.QueuePosition != 1 {
		t.Fatalf("Up result = %+v, want sleeping at position 1", result)
	}
	if got := store.Get(newChildName); got == nil || got.SleepSince.IsZero() || got.Status != contract.SessionStatusDown {
		t.Fatalf("session %q = %+v, want queued sleep", newChildName, got)
	}
}

func TestUp_ManualParentlessAdmissionQueuesAtVirtualRootCap(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	cfg.MaxUpChildren = intPtr(1)
	seedSessionWithNodes(t, store, "existingRoot", "acct", 1, "", upTasks())

	result, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/manual"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "sleeping" || result.QueuePosition != 1 {
		t.Fatalf("Up result = %+v, want queued sleep", result)
	}
	if got := store.Get("case_manual+capwf"); got == nil || got.SleepSince.IsZero() {
		t.Fatalf("session = %+v, want persisted sleep", got)
	}
}

func TestWakeQueuedSessions_UsesCapacityInQueueOrder(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "child_wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'", cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSessionWithNodes(t, store, "parent", "acct", 1, "parent_wf", nil)
	for _, name := range []string{"active", "first", "second"} {
		seedSessionWithNodes(t, store, name, "acct", 1, "child_wf", nil)
		setParent(t, store, name, "parent")
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "active"}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"first", "second"} {
		result, err := Up(cfg, store, UpParams{Identifier: name})
		if err != nil || result.Result != "sleeping" || result.QueuePosition != i+1 {
			t.Fatalf("up %s = %+v, %v", name, result, err)
		}
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "active"}); err != nil {
		t.Fatal(err)
	}
	seedSessionWithNodes(t, store, "newcomer", "acct", 1, "child_wf", nil)
	setParent(t, store, "newcomer", "parent")
	if newcomer, err := Up(cfg, store, UpParams{Identifier: "newcomer"}); err != nil || newcomer.Result != "sleeping" {
		t.Fatalf("newcomer = %+v, %v, want to wait behind first", newcomer, err)
	}
	if err := WakeQueuedSessions(cfg, store); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("first"); got.Status != contract.SessionStatusUp || !got.SleepSince.IsZero() {
		t.Fatalf("first = %+v, want awake", got)
	}
	if got := store.Get("second"); got.SleepSince.IsZero() {
		t.Fatalf("second = %+v, want queued", got)
	}
}

func TestUp_AllowsNewChildAfterSiblingDrops(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "childA", "acct", 0, "", upTasks())
	setParent(t, store, "childA", "parent1")
	seedSessionWithNodes(t, store, "childB", "acct", 1, "", nil)
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

func TestUp_NoCapDeclaredAllowsUnlimitedChildrenViaUp(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", nil)

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		seedSessionWithNodes(t, store, "child"+name, "acct", i, "", upTasks())
		setParent(t, store, "child"+name, "parent1")
	}

	url := "https://example.test/cases/many"
	if _, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "parent1"}); err != nil {
		t.Fatalf("Up: %v, want success (no cap declared)", err)
	}
}

func TestUp_ReRunOnAlreadyUpChildAtFullCapStaysIdempotent(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "child_wf",
		[]taskFixture{{id: "noop", scope: "run", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "childA", "acct", 0, "child_wf", map[string]*contract.TaskState{
		"noop": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "childA", "parent1")
	seedSessionWithNodes(t, store, "childB", "acct", 1, "child_wf", upTasks())
	setParent(t, store, "childB", "parent1")

	if _, err := Up(cfg, store, UpParams{Identifier: "childA"}); err != nil {
		t.Fatalf("Up (re-up on already-up child): %v, want success", err)
	}
}

func TestUp_ReleasesReservationAfterSuccessfulAdmission(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)

	if _, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/first", ParentSession: "parent1"}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	reserved, capErr := reserveChildCapSlot(cfg, store, "second", "parent1", false)
	if capErr != nil {
		t.Fatalf("reserveChildCapSlot after a completed Up: %v, want nil", capErr)
	}
	if !reserved {
		t.Error("reserved = false, want true: the first Up's reservation should have been released")
	}
}

func TestDestroy_ClearsStaleReservationForTheDestroyedChild(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "child_wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "childA", "acct", 0, "child_wf", nil)
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

// Exercises TestStore_ReserveUpSlotNeverExpiresALiveReservationRegardlessOfAge
// through the real Up() path, not just ReserveUpSlot in isolation.
func TestUp_LiveReservationBlocksSiblingThroughTheRealUpPath(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderCreatingWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)

	if _, err := store.ReserveUpSlot("childA", "parent1", approveAnyReservation); err != nil {
		t.Fatalf("simulate an in-progress reservation: %v", err)
	}

	result, err := Up(cfg, store, UpParams{Identifier: "https://example.test/cases/blocked", ParentSession: "parent1"})
	if err != nil || result.Result != "sleeping" {
		t.Fatalf("Up = %+v, %v, want accepted sleep while sibling reserves capacity", result, err)
	}
}

// Exercises TestReserveChildCapSlot_ConcurrentAttemptForTheSameChildIsRejected
// through real concurrent Up() calls (a doubled-up orchestrator dispatch, say).
func TestUp_ConcurrentSameChildAttemptsRejectAllButOne(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "capwf", capProviderSlowWorkspace("capwf", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(5))
	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)

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

// Exercises TestReserveChildCapSlot_ForceRecreateReservationStillBlocksASibling
// through the real Up() --force-recreate path.
func TestUp_ForceRecreateHoldsItsCapSlotThroughTheRebuild(t *testing.T) {
	store := testStore(t)
	oldWorkdir := filepath.Join(t.TempDir(), "old-workdir")
	newWorkdir := filepath.Join(t.TempDir(), "new-workdir")
	if err := os.MkdirAll(oldWorkdir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "capwf",
		[]taskFixture{{id: "noop", scope: "run", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	// Slow enough that the sibling attempt below reliably lands while the
	// teardown-and-rebuild is still in flight.
	writeSetupWorkflow(t, cfg, "capwf", capProviderSlowWorkspace("capwf", newWorkdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "childA", "acct", 0, "capwf", map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{contract.OutputKeyWorkspaceDir: oldWorkdir},
		},
		"noop": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "childA", "parent1")
	if err := store.Update("childA", func(s *domain.Session) error {
		s.WorkspaceDirPath = oldWorkdir
		return nil
	}); err != nil {
		t.Fatalf("seed workspace dir: %v", err)
	}
	seedSessionWithNodes(t, store, "childB", "acct", 1, "capwf", nil)
	setParent(t, store, "childB", "parent1")

	done := make(chan error, 1)
	go func() {
		_, err := Up(cfg, store, UpParams{Identifier: "childA", ForceRecreate: true})
		done <- err
	}()

	// Give the recreate time to reserve its slot and start tearing down
	// before the sibling attempts to admit.
	time.Sleep(150 * time.Millisecond)

	siblingResult, siblingErr := Up(cfg, store, UpParams{Identifier: "childB"})
	if siblingErr != nil || siblingResult.Result != "sleeping" {
		t.Fatalf("childB = %+v, %v, want accepted sleep", siblingResult, siblingErr)
	}

	if err := <-done; err != nil {
		t.Fatalf("Up --force-recreate: %v", err)
	}
}
