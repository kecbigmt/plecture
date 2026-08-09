package service

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/state"
	contract "github.com/kecbigmt/plect/contracts/state"
)

func seedSession(t *testing.T, store interface {
	Put(*domain.Session) error
}, sessionName, ownerRepo string, number int, workflow string, tasks map[string]*contract.TaskState) {
	t.Helper()
	now := time.Now()
	session := &domain.Session{
		Name:      sessionName,
		URL:       "https://github.com/" + ownerRepo + "/issues/1",
		URLType:   "issue",
		OwnerRepo: ownerRepo,
		Number:    number,
		Branch:    "issue/1",
		Workflow:  workflow,
		Tasks:     tasks,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Put(session); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func setParent(t *testing.T, store *state.Store, sessionName, parentName string) {
	t.Helper()
	if err := store.Update(sessionName, func(s *domain.Session) error {
		s.ParentSession = parentName
		return nil
	}); err != nil {
		t.Fatalf("set parent: %v", err)
	}
}

func TestResolveParentSession_RootPrefixAcceptsExistingTarget(t *testing.T) {
	store := testStore(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)

	got, err := resolveParentSession(store, "owner/repo-reviewer", "root:owner/repo-1")
	if err != nil {
		t.Fatalf("resolveParentSession: %v", err)
	}
	if got != "root:owner/repo-1" {
		t.Fatalf("got %q, want %q (stored literally, not resolved)", got, "root:owner/repo-1")
	}
}

func TestResolveParentSession_RootPrefixRejectsMissingTarget(t *testing.T) {
	store := testStore(t)

	_, err := resolveParentSession(store, "owner/repo-reviewer", "root:owner/repo-missing")
	if err == nil {
		t.Fatal("expected error when root: target session does not exist")
	}
	if err.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, ErrInvalidInput)
	}
}

func TestUp_RejectsInputWithBareSessionName(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}

	_, err := Up(cfg, store, UpParams{Identifier: "org/repo-1", Inputs: map[string]any{"template": "review"}})
	if err == nil {
		t.Fatal("expected error when --input is combined with a session name")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrInvalidInput)
	}
}

func TestUp_RejectsInputWhenSessionExists(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	sessionName := "org/repo-9"
	seedSession(t, store, sessionName, "org/repo", 9, "", nil)

	url := "https://github.com/org/repo/issues/9"
	_, err := Up(cfg, store, UpParams{Identifier: url, Inputs: map[string]any{"template": "review"}})
	if err == nil {
		t.Fatal("expected error when --input is passed against an existing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrInvalidInput)
	}
	if !strings.Contains(svcErr.Message, "destroy and recreate") {
		t.Errorf("Message should hint at destroy+recreate, got %q", svcErr.Message)
	}
}

func TestResolveSessionInput_NoSchemaAcceptsAnyObject(t *testing.T) {
	cfg := &config.Config{}
	got, err := resolveSessionInputs(cfg, "", "", map[string]any{"anything": "goes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["anything"] != "goes" {
		t.Errorf("got %v, want value preserved", got)
	}
}

// Regression: the schema source-id used to be "inline://workflow:<name>",
// which the URL parser inside jsonschema/v6 read as host + invalid port.
// Any workflow with a hyphenated name (e.g. "coding-claude") triggered it.
// IDs now use a custom `tws:` scheme (RFC 3986 `scheme:opaque`, no `//`),
// so the parser never tries to find a host.
func TestResolveSessionInputs_WorkflowSchemaCompilesWithHyphenatedName(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".tws", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".tws", "workflows", "coding-claude.toml"), []byte(`
name = "coding-claude"

[inputs_schema]
type = "object"
required = ["template"]

[inputs_schema.properties]
template = { type = "string" }

[[nodes]]
id = "envfile"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Schema-bearing layers must be trusted: resolve from a worktree one
	// level below the declaring overlay.
	worktreeDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	if _, err := resolveSessionInputs(cfg, worktreeDir, "coding-claude", map[string]any{"template": "review"}); err != nil {
		t.Fatalf("resolveSessionInputs: %v", err)
	}
}

func TestResolveSessionInput_NilWithSchemaValidatesEmptyObject(t *testing.T) {
	cfg := &config.Config{
		InputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"template"},
			"properties": map[string]any{
				"template": map[string]any{"type": "string"},
			},
		},
	}
	_, err := resolveSessionInputs(cfg, "", "", nil)
	if err == nil {
		t.Fatal("expected schema rejection for empty input")
	}
	if err.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, ErrInvalidInput)
	}
}

func TestResolveSessionInput_SchemaValidates(t *testing.T) {
	cfg := &config.Config{
		InputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"template"},
			"properties": map[string]any{
				"template": map[string]any{"type": "string", "enum": []any{"review", "respond"}},
			},
		},
	}

	if _, err := resolveSessionInputs(cfg, "", "", map[string]any{"template": "review"}); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	got, err := resolveSessionInputs(cfg, "", "", map[string]any{"template": "not-in-enum"})
	if err == nil {
		t.Fatalf("expected schema error, got value %v", got)
	}
	if err.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, ErrInvalidInput)
	}
}

func TestResolveSessionInput_InlineAndFileMutuallyExclusive(t *testing.T) {
	cfg := &config.Config{
		InputsSchema:     map[string]any{"type": "object"},
		InputsSchemaFile: "/tmp/x.json",
	}
	_, err := resolveSessionInputs(cfg, "", "", map[string]any{})
	if err == nil {
		t.Fatal("expected error for both inline and file declared")
	}
	if !strings.Contains(err.Message, "mutually exclusive") {
		t.Errorf("Message = %q, want mutually-exclusive wording", err.Message)
	}
}

// TestDestroy_DefaultFailsFastOnRunCleanupError exercises the default policy:
// without --force, a cleanup error must abort teardown and keep the state
// entry intact so the user can inspect / retry.
// A guarded orchestrator must not be able to tear down a session outside its
// name space — the symmetric vector to the Create guard. The guard fires
// before any cleanup or worktree removal, so the target session survives.
func TestDestroy_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{SessionGuard: "^acme/"}
	sessionName := "exampleorg/repo-26"
	seedSession(t, store, sessionName, "exampleorg/repo", 26, "default", nil)

	_, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Fatalf("want ErrRepoNotAllowed for cross-owner destroy, got %v", err)
	}
	if store.Get(sessionName) == nil {
		t.Fatal("rejected destroy must leave the target session intact")
	}
}

// Down is a per-session write too — the guard clamps it the same way.
func TestDown_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{SessionGuard: "^acme/"}
	sessionName := "exampleorg/repo-26"
	seedSession(t, store, sessionName, "exampleorg/repo", 26, "default", nil)

	_, err := Down(cfg, store, DownParams{Identifier: sessionName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Fatalf("want ErrRepoNotAllowed for cross-owner down, got %v", err)
	}
}

// checkLifecycleRelationGuard is exercised directly (no cfg/workflow needed)
// against every tree-relation case, including the implicit-root sibling
// wrinkle: a session opted into `--parent root:X` is X's sibling, not X's
// descendant, so X must not be able to destroy it.
func TestCheckLifecycleRelationGuard(t *testing.T) {
	newTreeStore := func(t *testing.T) *state.Store {
		store := testStore(t)
		seedSession(t, store, "org/repo-parent", "org/repo", 1, "default", nil)
		seedSession(t, store, "org/repo-child", "org/repo", 2, "default", nil)
		seedSession(t, store, "org/repo-grandchild", "org/repo", 3, "default", nil)
		seedSession(t, store, "org/repo-unrelated", "org/repo", 4, "default", nil)
		setParent(t, store, "org/repo-child", "org/repo-parent")
		setParent(t, store, "org/repo-grandchild", "org/repo-child")
		return store
	}

	t.Run("self is allowed", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "org/repo-parent")
		if err := checkLifecycleRelationGuard(store, "org/repo-parent", "destroy"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("direct parent destroying direct child is allowed", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "org/repo-parent")
		if err := checkLifecycleRelationGuard(store, "org/repo-child", "destroy"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("ancestor destroying a multi-level descendant is allowed", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "org/repo-parent")
		if err := checkLifecycleRelationGuard(store, "org/repo-grandchild", "destroy"); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("child destroying its parent is rejected", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "org/repo-child")
		svcErr := checkLifecycleRelationGuard(store, "org/repo-parent", "destroy")
		if svcErr == nil || svcErr.Code != ErrRelationNotAllowed {
			t.Fatalf("want ErrRelationNotAllowed, got %v", svcErr)
		}
	})

	t.Run("unrelated session is rejected", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "org/repo-unrelated")
		svcErr := checkLifecycleRelationGuard(store, "org/repo-parent", "destroy")
		if svcErr == nil || svcErr.Code != ErrRelationNotAllowed {
			t.Fatalf("want ErrRelationNotAllowed, got %v", svcErr)
		}
	})

	t.Run("implicit-root sibling reviewer cannot destroy the parentless session", func(t *testing.T) {
		store := testStore(t)
		seedSession(t, store, "org/repo-owner", "org/repo", 1, "default", nil) // parentless
		seedSession(t, store, "org/repo-reviewer", "org/repo", 2, "default", nil)
		setParent(t, store, "org/repo-reviewer", "root:org/repo-owner")
		t.Setenv("TWS_SESSION_NAME", "org/repo-reviewer")
		svcErr := checkLifecycleRelationGuard(store, "org/repo-owner", "destroy")
		if svcErr == nil || svcErr.Code != ErrRelationNotAllowed {
			t.Fatalf("want ErrRelationNotAllowed (sibling via implicit root), got %v", svcErr)
		}
	})

	t.Run("no ambient session is exempt (human CLI recovery path)", func(t *testing.T) {
		store := newTreeStore(t)
		t.Setenv("TWS_SESSION_NAME", "")
		if err := checkLifecycleRelationGuard(store, "org/repo-unrelated", "destroy"); err != nil {
			t.Fatalf("want nil (no ambient session is exempt), got %v", err)
		}
	})
}

// TestDestroy_RelationGuardBlocksUnrelatedCaller and TestDown_RelationGuardBlocksUnrelatedCaller
// exercise the guard through the service entry points: an orchestrator must
// not destroy/down a session outside its own subtree, even one it can see
// via `tws ls`.
func TestDestroy_RelationGuardBlocksUnrelatedCaller(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{}
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", nil)
	seedSession(t, store, "org/repo-caller", "org/repo", 2, "default", nil)
	t.Setenv("TWS_SESSION_NAME", "org/repo-caller")

	_, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRelationNotAllowed {
		t.Fatalf("want ErrRelationNotAllowed for unrelated destroy, got %v", err)
	}
	if store.Get(sessionName) == nil {
		t.Fatal("rejected destroy must leave the target session intact")
	}
}

func TestDown_RelationGuardBlocksUnrelatedCaller(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{}
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", nil)
	seedSession(t, store, "org/repo-caller", "org/repo", 2, "default", nil)
	t.Setenv("TWS_SESSION_NAME", "org/repo-caller")

	_, err := Down(cfg, store, DownParams{Identifier: sessionName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRelationNotAllowed {
		t.Fatalf("want ErrRelationNotAllowed for unrelated down, got %v", err)
	}
}

// TestDestroy_RelationGuardAllowsSelf confirms the guard doesn't just reject
// correctly but also lets a legitimate self-teardown proceed to completion.
func TestDestroy_RelationGuardAllowsSelf(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "envfile", scope: "session", setup: "true", cleanup: "true"}},
		[]nodeFixture{{id: "envfile"}},
	)
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", map[string]*contract.TaskState{
		"envfile": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})
	t.Setenv("TWS_SESSION_NAME", sessionName)

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Destroy (self): %v", err)
	}
	if store.Get(sessionName) != nil {
		t.Fatal("expected state entry deleted after self-destroy")
	}
}

// `tws up <bare-existing-session>` skips the guarded auto-create path, so the
// guard at the existing-session resolution must catch it too.
func TestUp_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{SessionGuard: "^acme/"}
	sessionName := "exampleorg/repo-26"
	seedSession(t, store, sessionName, "exampleorg/repo", 26, "default", nil)

	_, err := Up(cfg, store, UpParams{Identifier: sessionName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Fatalf("want ErrRepoNotAllowed for cross-owner up, got %v", err)
	}
}

// A run-scope node's setup script (e.g. goal_bootstrap re-deriving
// `pursue_goal` instances during `tws up`, see
// config/tws/tasks/goal_bootstrap.toml) can itself shell out to a nested
// `tws task setup`, which writes its instance straight to state.json while
// this Up call's own RunSetup is still in flight. Up's persist must overlay
// (mergeTasks), not blind-Put, or that nested write is clobbered — the same
// hazard TestCreate_SessionNodeNestedWriteSurvives covers for Create's
// initial_task dispatcher. Exercises the real store path (not a stub).
func TestUp_RunScopeNestedWriteSurvives(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	store := testStore(t)
	sessionName := "org/repo-12"
	t.Setenv("SP", filepath.Join(store.Dir(), "state.json"))

	// dispatcher mimics a nested `tws task setup`: writes a sibling "goal_x"
	// key straight to disk, then produces normally.
	dispatcher := fmt.Sprintf(`jq '.sessions["%s"].tasks.goal_x={"scope":"session","status":"produced","dynamic":true,"task_id":"pursue_goal","name":"goal_x","outputs":{}}' "$SP" > "$SP.tmp" && mv "$SP.tmp" "$SP"
echo '{}'`, sessionName)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "dispatcher", scope: "run", setup: dispatcher}},
		[]nodeFixture{{id: "dispatcher"}},
	)
	seedSession(t, store, sessionName, "org/repo", 12, "default", nil)

	result, err := Up(cfg, store, UpParams{Identifier: sessionName})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// The dispatcher node itself produced…
	if st := result.Tasks["dispatcher"]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("dispatcher node = %+v, want produced", st)
	}
	// …and its nested write survived Up's persist, both in the result and on disk.
	got := result.Tasks["goal_x"]
	if got == nil {
		t.Fatal("nested-written 'goal_x' instance was clobbered by the up persist (result)")
	}
	if got.Name != "goal_x" || got.TaskID != "pursue_goal" {
		t.Fatalf("goal_x instance mismatch: %+v", got)
	}
	persisted := store.Get(sessionName)
	if persisted == nil || persisted.Tasks["goal_x"] == nil {
		t.Fatal("nested-written 'goal_x' instance was clobbered by the up persist (disk)")
	}
}

func TestDestroy_WritesTombstone(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "envfile", scope: "session", setup: "true", cleanup: "true"},
		},
		[]nodeFixture{{id: "envfile"}},
	)
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", map[string]*contract.TaskState{
		"envfile": {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"path": "/tmp/env"},
		},
	})

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if store.Get(sessionName) != nil {
		t.Fatal("expected state entry deleted after destroy")
	}

	data, ok, err := eventlog.NewStore(store.Dir()).ReadTombstone(sessionName)
	if err != nil {
		t.Fatalf("ReadTombstone: %v", err)
	}
	if !ok {
		t.Fatal("expected a tombstone to exist after destroy")
	}
	var tomb contract.Tombstone
	if err := json.Unmarshal(data, &tomb); err != nil {
		t.Fatalf("unmarshal tombstone: %v", err)
	}
	if tomb.Name != sessionName {
		t.Errorf("tombstone.Name = %q, want %q", tomb.Name, sessionName)
	}
	if tomb.DestroyedAt.IsZero() {
		t.Error("expected DestroyedAt to be set")
	}
	st := tomb.Tasks["envfile"]
	if st == nil || st.Outputs["path"] != "/tmp/env" {
		t.Errorf("expected envfile outputs preserved in tombstone, got %+v", tomb.Tasks)
	}
}

func TestDestroy_DefaultFailsFastOnRunCleanupError(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: "true", cleanup: "exit 1"},
			{id: "envfile", scope: "session", setup: "true", cleanup: "true"},
		},
		[]nodeFixture{{id: "tmux"}, {id: "envfile"}},
	)
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", map[string]*contract.TaskState{
		"tmux":    {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		"envfile": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})

	_, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName})
	if err == nil {
		t.Fatal("expected error without --force")
	}
	if got := store.Get(sessionName); got == nil {
		t.Fatal("expected state entry preserved for retry, was deleted")
	}
}

// TestDestroy_ForceContinuesOnCleanupError exercises the --force policy:
// cleanup errors are recorded as warnings, session-scoped cleanup still runs
// after a run-scoped failure, and the state entry is deleted.
func TestDestroy_ForceContinuesOnCleanupError(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	tmpDir := t.TempDir()
	marker := tmpDir + "/session-cleanup-ran"
	cfg := writeWorkflowFixture(t, tmpDir, "default",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: "true", cleanup: "exit 1"},
			{id: "envfile", scope: "session", setup: "true", cleanup: "touch " + marker},
		},
		[]nodeFixture{{id: "tmux"}, {id: "envfile"}},
	)
	sessionName := "org/repo-2"
	seedSession(t, store, sessionName, "org/repo", 2, "default", map[string]*contract.TaskState{
		"tmux":    {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		"envfile": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})

	result, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName, Force: true})
	if err != nil {
		t.Fatalf("Destroy --force returned error, want nil: %v", err)
	}
	if len(result.CleanupWarnings) == 0 {
		t.Fatal("expected CleanupWarnings to record run cleanup failure")
	}
	// The teardown is a single reverse-instantiation pass (ADR-003), so the
	// warning is prefixed "cleanup:" rather than the old per-scope label; the
	// failing task (tmux) is named in it.
	if joined := strings.Join(result.CleanupWarnings, "|"); !strings.Contains(joined, "tmux") {
		t.Fatalf("expected tmux cleanup failure in warnings, got %q", joined)
	}
	if _, err := exec.Command("bash", "-c", "test -f "+marker).CombinedOutput(); err != nil {
		t.Fatalf("expected session cleanup marker at %s, missing", marker)
	}
	if got := store.Get(sessionName); got != nil {
		t.Fatalf("expected state entry deleted, still present: %+v", got)
	}
}

// TestDestroy_BlocksWhenChildrenExist covers the fail-closed guard:
// store.Delete unconditionally clears a child's ParentSession, so destroying
// a session with children must abort before any teardown step runs.
func TestDestroy_BlocksWhenChildrenExist(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{}
	parentName := "org/repo-1"
	childName := "org/repo-2"
	seedSession(t, store, parentName, "org/repo", 1, "default", nil)
	seedSession(t, store, childName, "org/repo", 2, "default", nil)
	setParent(t, store, childName, parentName)

	_, err := Destroy(cfg, store, DestroyParams{Identifier: parentName})
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrHasChildren {
		t.Fatalf("want ErrHasChildren, got %v", err)
	}
	if !strings.Contains(svcErr.Message, childName) {
		t.Errorf("expected child session name in error message, got %q", svcErr.Message)
	}
	if store.Get(parentName) == nil {
		t.Fatal("rejected destroy must leave the parent session intact")
	}
	if got := store.Get(childName); got == nil || got.ParentSession != parentName {
		t.Fatal("rejected destroy must leave the child's ParentSession intact")
	}
}

// TestDestroy_ForceOrphansChildrenWithWarning covers the --force path: the
// parent is destroyed as before, but the now-orphaned child is called out in
// CleanupWarnings instead of vanishing silently.
func TestDestroy_ForceOrphansChildrenWithWarning(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "envfile", scope: "session", setup: "true", cleanup: "true"}},
		[]nodeFixture{{id: "envfile"}},
	)
	parentName := "org/repo-1"
	childName := "org/repo-2"
	seedSession(t, store, parentName, "org/repo", 1, "default", map[string]*contract.TaskState{
		"envfile": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})
	seedSession(t, store, childName, "org/repo", 2, "default", nil)
	setParent(t, store, childName, parentName)

	result, err := Destroy(cfg, store, DestroyParams{Identifier: parentName, Force: true})
	if err != nil {
		t.Fatalf("Destroy --force returned error, want nil: %v", err)
	}
	if joined := strings.Join(result.CleanupWarnings, "|"); !strings.Contains(joined, childName) {
		t.Fatalf("expected orphaned child %q in CleanupWarnings, got %q", childName, joined)
	}
	if store.Get(parentName) != nil {
		t.Fatal("expected parent state entry deleted")
	}
	got := store.Get(childName)
	if got == nil {
		t.Fatal("expected child session to still exist (orphaned, not deleted)")
	}
	if got.ParentSession != "" {
		t.Errorf("expected child ParentSession cleared after orphaning, got %q", got.ParentSession)
	}
}
