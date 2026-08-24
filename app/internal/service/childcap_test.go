package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// writeCapWorkflow declares a workflow with no nodes, optionally carrying
// `max_up_children`. reserveChildCapSlot only ever reads the parent's
// workflow for this one field, so a parent fixture needs nothing else.
func writeCapWorkflow(t *testing.T, baseDir, id string, maxUpChildren *int) {
	t.Helper()
	dir := filepath.Join(baseDir, "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("[%s]\nkind = \"workflow\"\n", id)
	if maxUpChildren != nil {
		body += fmt.Sprintf("max_up_children = %d\n", *maxUpChildren)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// upTasks is a single produced run-scoped task, the fact sessionRunState
// reads as "up".
func upTasks() map[string]*contract.TaskState {
	return map[string]*contract.TaskState{
		"run_node": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	}
}

func intPtr(n int) *int { return &n }

func TestReserveChildCapSlot_NoCapDeclaredAllowsUnlimitedChildren(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", nil)
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("child%d", i)
		seedSession(t, store, name, "acct", i, "", upTasks())
		setParent(t, store, name, "parent1")
	}

	reserved, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (no cap declared)", err)
	}
	if reserved {
		t.Error("reserved = true, want false: no cap declared means nothing to reserve against")
	}
}

func TestReserveChildCapSlot_UnderCapAllowsNewChild(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSession(t, store, "child0", "acct", 0, "", upTasks())
	setParent(t, store, "child0", "parent1")

	reserved, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (1 up child, cap 2)", err)
	}
	if !reserved {
		t.Error("reserved = false, want true: a slot was free")
	}
}

func TestReserveChildCapSlot_AtCapRejectsNewChild(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("child%d", i)
		seedSession(t, store, name, "acct", i, "", upTasks())
		setParent(t, store, name, "parent1")
	}

	reserved, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if reserved {
		t.Error("reserved = true, want false: parent is already at cap")
	}
	if err == nil {
		t.Fatal("err = nil, want ErrChildCapExceeded (2 up children, cap 2)")
	}
	if err.Code != ErrChildCapExceeded {
		t.Errorf("Code = %q, want %q", err.Code, ErrChildCapExceeded)
	}
	for _, want := range []string{"parent1", "2"} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("Message = %q, want it to name %q (parent/cap/count)", err.Message, want)
		}
	}
}

func TestReserveChildCapSlot_DownChildDoesNotCountTowardCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSession(t, store, "childUp", "acct", 0, "", upTasks())
	setParent(t, store, "childUp", "parent1")
	// A down child (no produced run-scoped task) must not count toward the cap.
	seedSession(t, store, "childDown", "acct", 1, "", nil)
	setParent(t, store, "childDown", "parent1")

	reserved, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (1 up + 1 down, cap 2)", err)
	}
	if !reserved {
		t.Error("reserved = false, want true: the down child left a slot free")
	}
}

func TestReserveChildCapSlot_AlreadyUpTargetIsExemptEvenAtFullCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("child%d", i)
		seedSession(t, store, name, "acct", i, "", upTasks())
		setParent(t, store, name, "parent1")
	}

	// A re-up on an already-up child must stay idempotent, not be rejected by
	// the cap it is itself already counted under — and must not reserve a
	// slot, since it never releases one either.
	reserved, err := reserveChildCapSlot(cfg, store, "parent1", true)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (already-up target is exempt)", err)
	}
	if reserved {
		t.Error("reserved = true, want false: an already-up target needs no reservation")
	}
}

func TestReserveChildCapSlot_NoParentSkipsCheck(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}

	reserved, err := reserveChildCapSlot(cfg, store, "", false)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (no parent, e.g. a root session)", err)
	}
	if reserved {
		t.Error("reserved = true, want false")
	}
}

func TestReserveChildCapSlot_UnknownParentSkipsCheck(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}

	// The "root:<target>" pseudo-parent form (see resolveParentSession)
	// stores a literal string with no corresponding session state; a cap
	// declared on a workflow can only bind a session that actually exists.
	reserved, err := reserveChildCapSlot(cfg, store, "root:external", false)
	if err != nil {
		t.Fatalf("reserveChildCapSlot: %v, want nil (parent has no state entry)", err)
	}
	if reserved {
		t.Error("reserved = true, want false")
	}
}

// A reservation the target's own state doesn't reflect yet still must count
// toward the cap, or a second decision computed before the first
// reservation's child actually goes up would admit past the limit.
func TestReserveChildCapSlot_OutstandingReservationCountsTowardCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)

	first, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if err != nil || !first {
		t.Fatalf("first reservation: reserved=%v err=%v, want true/nil", first, err)
	}

	second, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if second {
		t.Error("second reservation succeeded despite the first still being outstanding")
	}
	if err == nil || err.Code != ErrChildCapExceeded {
		t.Fatalf("second reservation err = %v, want ErrChildCapExceeded", err)
	}

	releaseChildCapSlot(store, "parent1")

	third, err := reserveChildCapSlot(cfg, store, "parent1", false)
	if err != nil || !third {
		t.Fatalf("reservation after release: reserved=%v err=%v, want true/nil", third, err)
	}
}

// The atomicity guarantee itself: concurrent reservations against the same
// capped parent must never admit more than the declared limit, however many
// callers race it at once.
func TestReserveChildCapSlot_ConcurrentReservationsRespectCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	const limit = 3
	const attempts = 20
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(limit))
	seedSession(t, store, "parent1", "acct", 1, "parent_wf", nil)

	var wg sync.WaitGroup
	results := make([]bool, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			reserved, err := reserveChildCapSlot(cfg, store, "parent1", false)
			if err != nil && err.Code != ErrChildCapExceeded {
				t.Errorf("reserveChildCapSlot: unexpected error %v", err)
				return
			}
			results[i] = reserved
		}(i)
	}
	wg.Wait()

	admitted := 0
	for _, ok := range results {
		if ok {
			admitted++
		}
	}
	if admitted != limit {
		t.Fatalf("admitted = %d, want exactly %d (the declared limit) across %d concurrent attempts", admitted, limit, attempts)
	}
}
