package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// writeCapWorkflow declares a workflow with no nodes, optionally carrying
// `max_up_children`. checkChildCap only ever reads the parent's workflow for
// this one field, so a parent fixture needs nothing else.
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

func TestCheckChildCap_NoCapDeclaredAllowsUnlimitedChildren(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", nil)
	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("org/child-%d", i)
		seedSession(t, store, name, "org/child", i, "", upTasks())
		setParent(t, store, name, "org/parent-1")
	}

	if err := checkChildCap(cfg, store, "org/parent-1", false); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (no cap declared)", err)
	}
}

func TestCheckChildCap_UnderCapAllowsNewChild(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	seedSession(t, store, "org/child-0", "org/child", 0, "", upTasks())
	setParent(t, store, "org/child-0", "org/parent-1")

	if err := checkChildCap(cfg, store, "org/parent-1", false); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (1 up child, cap 2)", err)
	}
}

func TestCheckChildCap_AtCapRejectsNewChild(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("org/child-%d", i)
		seedSession(t, store, name, "org/child", i, "", upTasks())
		setParent(t, store, name, "org/parent-1")
	}

	err := checkChildCap(cfg, store, "org/parent-1", false)
	if err == nil {
		t.Fatal("checkChildCap = nil, want ErrChildCapExceeded (2 up children, cap 2)")
	}
	if err.Code != ErrChildCapExceeded {
		t.Errorf("Code = %q, want %q", err.Code, ErrChildCapExceeded)
	}
	for _, want := range []string{"org/parent-1", "2"} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("Message = %q, want it to name %q (parent/cap/count)", err.Message, want)
		}
	}
}

func TestCheckChildCap_DownChildDoesNotCountTowardCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	seedSession(t, store, "org/child-up", "org/child", 0, "", upTasks())
	setParent(t, store, "org/child-up", "org/parent-1")
	// A down child (no produced run-scoped task) must not count toward the cap.
	seedSession(t, store, "org/child-down", "org/child", 1, "", nil)
	setParent(t, store, "org/child-down", "org/parent-1")

	if err := checkChildCap(cfg, store, "org/parent-1", false); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (1 up + 1 down, cap 2)", err)
	}
}

func TestCheckChildCap_AlreadyUpTargetIsExemptEvenAtFullCap(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))
	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("org/child-%d", i)
		seedSession(t, store, name, "org/child", i, "", upTasks())
		setParent(t, store, name, "org/parent-1")
	}

	// A re-up on an already-up child must stay idempotent, not be rejected by
	// the cap it is itself already counted under.
	if err := checkChildCap(cfg, store, "org/parent-1", true); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (already-up target is exempt)", err)
	}
}

func TestCheckChildCap_NoParentSkipsCheck(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}

	if err := checkChildCap(cfg, store, "", false); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (no parent, e.g. a root session)", err)
	}
}

func TestCheckChildCap_UnknownParentSkipsCheck(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir()}

	// The "root:<target>" pseudo-parent form (see resolveParentSession)
	// stores a literal string with no corresponding session state; a cap
	// declared on a workflow can only bind a session that actually exists.
	if err := checkChildCap(cfg, store, "root:org/repo-1", false); err != nil {
		t.Fatalf("checkChildCap = %v, want nil (parent has no state entry)", err)
	}
}
