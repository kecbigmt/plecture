package service

import (
	"path/filepath"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Given a parent whose workflow declares max_up_children = 2 with 2 children
// up, when `plect up` would add a third child, then the command fails
// naming the parent, cap, and current count, and no session/state entry is
// created.
func TestUp_RejectsThirdChildAtCapAndCreatesNoStateEntry(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "gh", providerCreatingWorkspace("gh", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	for i, name := range []string{"org/child-a", "org/child-b"} {
		seedSession(t, store, name, "org/child", i, "", upTasks())
		setParent(t, store, name, "org/parent-1")
	}

	url := "https://github.com/org/repo/issues/77"
	newChildName := "org/repo-77+gh"
	_, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "org/parent-1"})
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
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "gh", providerCreatingWorkspace("gh", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(2))

	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	seedSession(t, store, "org/child-a", "org/child", 0, "", upTasks())
	setParent(t, store, "org/child-a", "org/parent-1")
	// org/child-b is down (destroyed would remove the entry outright; downed
	// is the case with a state entry but no produced run-scoped task).
	seedSession(t, store, "org/child-b", "org/child", 1, "", nil)
	setParent(t, store, "org/child-b", "org/parent-1")

	url := "https://github.com/org/repo/issues/78"
	result, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "org/parent-1"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	s := store.Get(result.SessionName)
	if s == nil {
		t.Fatal("new child session not persisted")
	}
	if s.ParentSession != "org/parent-1" {
		t.Errorf("ParentSession = %q, want org/parent-1", s.ParentSession)
	}
}

// Given a workflow with no cap declared, when any number of children are
// brought up, then behavior is unchanged from today.
func TestUp_NoCapDeclaredAllowsUnlimitedChildrenViaUp(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, filepath.Join(t.TempDir(), "workdirs"), "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "gh", providerCreatingWorkspace("gh", workdir))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", nil)

	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		seedSession(t, store, "org/child-"+name, "org/child", i, "", upTasks())
		setParent(t, store, "org/child-"+name, "org/parent-1")
	}

	url := "https://github.com/org/repo/issues/79"
	if _, err := Up(cfg, store, UpParams{Identifier: url, ParentSession: "org/parent-1"}); err != nil {
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

	seedSession(t, store, "org/parent-1", "org/parent", 1, "parent_wf", nil)
	seedSession(t, store, "org/child-a", "org/child", 0, "child_wf", map[string]*contract.TaskState{
		"noop": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "org/child-a", "org/parent-1")
	seedSession(t, store, "org/child-b", "org/child", 1, "child_wf", upTasks())
	setParent(t, store, "org/child-b", "org/parent-1")

	if _, err := Up(cfg, store, UpParams{Identifier: "org/child-a"}); err != nil {
		t.Fatalf("Up (re-up on already-up child): %v, want success", err)
	}
}
