package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
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

// capResourceDoc declares the resource type this file's fixtures accept.
const capResourceDoc = `[cases]
kind = "resource"
` + capResolverFields + `
[cases.observe]
type   = "shell"
script = "echo '{}'"
`

// useCapWorkflow points the fixture workflow at the cases resource and gives
// it a directory-creating effect; slow widens the window a concurrent Up()
// for the same child has to land its own reservation while this one is in
// flight.
func useCapWorkflow(t *testing.T, cfg *config.Config, wfID, workdir string, slow bool) {
	t.Helper()
	useResource(t, cfg, wfID, "cases", capResourceDoc)
	script := mkdirScript(workdir)
	if slow {
		script = "sleep 0.5\n" + script
	}
	attachWorkdir(t, cfg, wfID, taskFixture{setup: script})
}

func TestUp_AcceptsThirdChildAtCapAsQueuedSleep(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "child_wf",
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

func TestUp_IgnoresQueuedSessionsThatCannotWake(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *state.Store, *config.Config)
	}{
		{"suspended", func(t *testing.T, store *state.Store, cfg *config.Config) {
			for i := 0; i < 4; i++ {
				if err := WakeQueuedSessions(cfg, store); err != nil {
					t.Fatal(err)
				}
			}
			if reason, _, err := WakeSuspension(store, "broken"); err != nil || reason == "" {
				t.Fatalf("wake suspension = %q, %v", reason, err)
			}
		}},
		{"finalized", func(t *testing.T, store *state.Store, _ *config.Config) {
			if err := store.Update("broken", func(s *domain.Session) error {
				s.Tasks = map[string]*contract.TaskState{"work": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, FinalizedAt: time.Now()}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			cfg := writeWorkflowFixture(t, "child_wf",
				[]taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'"}},
				[]nodeFixture{{id: "runtime"}})
			writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
			seedSessionWithNodes(t, store, "parent", "acct", 1, "parent_wf", nil)
			seedSessionWithNodes(t, store, "broken", "acct", 2, "child_wf", nil)
			if tc.name == "suspended" {
				if err := store.Update("broken", func(s *domain.Session) error {
					s.Workflow = "missing_wf"
					s.WorkflowRevision, s.WorkflowRevisionBody = "", ""
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			seedSessionWithNodes(t, store, "ok", "acct", 3, "child_wf", nil)
			setParent(t, store, "broken", "parent")
			setParent(t, store, "ok", "parent")
			if _, err := queueSleep(store, "broken", "capacity", time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			tc.prepare(t, store, cfg)
			if position, err := SleepQueuePosition(cfg, store, "broken"); err != nil || position != 0 {
				t.Fatalf("blocked session queue position = %d, %v; want zero", position, err)
			}
			result, err := Up(cfg, store, UpParams{Identifier: "ok"})
			if err != nil || result.Result != "up" {
				t.Fatalf("Up(ok) = %+v, %v; want up with free capacity", result, err)
			}
			if tc.name == "suspended" {
				result, err := Up(cfg, store, UpParams{Identifier: "broken", HumanInitiated: true})
				if err != nil || result.Result != "sleeping" || result.QueuePosition != 1 {
					t.Fatalf("manual Up(broken) = %+v, %v; want a fresh queued request", result, err)
				}
			}
		})
	}
}

func TestWakeQueuedSessions_DoesNotRestartFullStoppedSnapshotEntry(t *testing.T) {
	store := testStore(t)
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	setup := fmt.Sprintf("touch %q; while [ ! -e %q ]; do sleep 0.01; done; echo '{}'", started, release)
	cfg := writeWorkflowFixture(t, "child_wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: setup, cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}})
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSessionWithNodes(t, store, "parent", "acct", 1, "parent_wf", nil)
	for _, name := range []string{"first", "second"} {
		seedSessionWithNodes(t, store, name, "acct", 1, "child_wf", nil)
		setParent(t, store, name, "parent")
	}
	if _, err := queueSleep(store, "first", "capacity", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := queueSleep(store, "second", "capacity", time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- WakeQueuedSessions(cfg, store) }()
	defer os.WriteFile(release, nil, 0o644)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first wake did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := store.Get("second"); got.Status != contract.SessionStatusDown || !got.SleepSince.IsZero() {
		t.Fatalf("full-stopped session restarted: %+v", got)
	}
}

func TestUp_AllowsNewChildAfterSiblingDrops(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "child_wf",
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
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "child_wf",
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
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, false)
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
	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	useCapWorkflow(t, cfg, "capwf", workdir, true)
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

	cfg := writeWorkflowFixture(t, "capwf",
		[]taskFixture{{id: "noop", scope: "run", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	// Slow enough that the sibling attempt below reliably lands while the
	// teardown-and-rebuild is still in flight.
	useCapWorkflow(t, cfg, "capwf", newWorkdir, true)
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))

	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "childA", "acct", 0, "capwf", map[string]*contract.TaskState{
		workdirEffectID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": oldWorkdir},
		},
		"noop": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "childA", "parent1")
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
