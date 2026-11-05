package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestSessionDisplayUsesStoredWorkflowDeclaration(t *testing.T) {
	revision := config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Display: map[string]*lang.Value{
		"title": {Form: lang.FormLiteral, Literal: "original"},
	}}}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	session := &domain.Session{Workflow: "work", WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
	var display cachedInfo
	applyDisplay(session, &display)
	if display.Title != "original" {
		t.Fatalf("display title = %q, want stored declaration", display.Title)
	}
}

func TestBuildPlanForSessionUsesRevisionAfterDefinitionsDisappear(t *testing.T) {
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "original", scope: "run", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "original"}},
	)
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "workflows", "coding.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "tasks", "original.toml")); err != nil {
		t.Fatal(err)
	}
	session := &domain.Session{Workflow: "coding", WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
	plan, err := buildPlanForSession(cfg, session)
	if err != nil || len(plan.Run) != 1 || plan.Run[0].NodeID != "original" {
		t.Fatalf("stored plan = %+v, err %v", plan, err)
	}
}

func TestBuildPlanForSessionKeepsExternalSchemaAfterFileRemoved(t *testing.T) {
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "original", scope: "run", setup: `echo '{}'`, extra: "inputs_schema_file = \"input-schema.json\"\n"}},
		[]nodeFixture{{id: "original"}},
	)
	schemaPath := filepath.Join(cfg.BaseDir, "tasks", "input-schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(schemaPath); err != nil {
		t.Fatal(err)
	}
	session := &domain.Session{Workflow: "coding", WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
	plan, err := buildPlanForSession(cfg, session)
	if err != nil || plan == nil || len(plan.Run) != 1 {
		t.Fatalf("stored plan with external schema = %+v, err %v", plan, err)
	}
}

func TestBackfillWorkflowRevisionsPinsExistingSessionAtMigration(t *testing.T) {
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "original", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "original"}},
	)
	store := testStore(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "existing", Workflow: "coding", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := BackfillWorkflowRevisions(cfg, store); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "workflows", "coding.toml")); err != nil {
		t.Fatal(err)
	}
	session := store.Get("existing")
	if session.WorkflowRevision == "" {
		t.Fatal("migration did not assign a revision")
	}
	plan, err := buildPlanForSession(cfg, session)
	if err != nil || plan == nil || len(plan.Run) != 1 {
		t.Fatalf("migrated plan = %+v, err %v", plan, err)
	}
}

func TestDestroyForceRemovesFailedSetupWithoutRevision(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{
		Name: "failed-setup", Workflow: "removed", Status: contract.SessionStatusDown,
		CreatedAt: now, UpdatedAt: now,
		Nodes: map[string]*contract.TaskState{workdirEffectID: {Status: contract.TaskStatusFailed, Scope: contract.TaskScopeSession}},
	}
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	if _, err := Destroy(&config.Config{}, store, DestroyParams{Identifier: session.Name}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("non-force destroy error = %v, want --force remedy", err)
	}
	if _, err := Destroy(&config.Config{}, store, DestroyParams{Identifier: session.Name, Force: true}); err != nil {
		t.Fatalf("force destroy failed: %v", err)
	}
	if got := store.Get(session.Name); got != nil {
		t.Fatalf("session remains: %+v", got)
	}
}

func TestDestroyFailedEffectSetupUsesCapturedCleanupAfterEffectEdit(t *testing.T) {
	root := t.TempDir()
	allocation := filepath.Join(root, "allocation")
	cleanupLog := filepath.Join(root, "cleanup.log")
	cfg := writeWorkflowFixture(t, "wf", nil, nil)
	attachWorkdir(t, cfg, "wf", taskFixture{
		setup:   fmt.Sprintf("mkdir -p %q; exit 1", allocation),
		cleanup: fmt.Sprintf("rm -rf %q; printf original > %q", allocation, cleanupLog),
	})
	store := testStore(t)
	resource := "https://github.com/org/repo/issues/5"
	_, createErr := Create(cfg, store, CreateParams{URL: resource})
	if createErr == nil {
		t.Fatal("Create succeeded despite failed effect setup")
	}
	sessions, err := store.AllE()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("failed setup sessions = %+v, err %v", sessions, err)
	}
	var session *domain.Session
	for _, candidate := range sessions {
		session = candidate
	}
	if session == nil || session.Nodes[workdirEffectID] == nil {
		t.Fatalf("failed setup session = %+v, create error = %v", session, createErr)
	}
	if _, err := os.Stat(allocation); err != nil {
		t.Fatalf("setup allocation: %v", err)
	}
	changed := effectFixtureDoc(taskFixture{
		id: workdirEffectID, scope: "session", setup: "exit 1",
		cleanup: fmt.Sprintf("printf changed > %q", cleanupLog),
	})
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", workdirEffectID+".toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: session.Name}); err != nil {
		t.Fatalf("Destroy failed setup: %v", err)
	}
	if _, err := os.Stat(allocation); !os.IsNotExist(err) {
		t.Fatalf("allocation remains after cleanup: %v", err)
	}
	if got, err := os.ReadFile(cleanupLog); err != nil || string(got) != "original" {
		t.Fatalf("cleanup = %q, err %v, want captured effect", got, err)
	}
}

func TestCreateRetryKeepsEffectRevisionAfterFailedSetup(t *testing.T) {
	root := t.TempDir()
	firstAttempt := filepath.Join(root, "attempted")
	workspace := filepath.Join(root, "workspace")
	cleanupLog := filepath.Join(root, "cleanup.log")
	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	attachWorkdir(t, cfg, "wf", taskFixture{
		setup:   fmt.Sprintf("if [ ! -e %q ]; then touch %q; exit 1; fi\n", firstAttempt, firstAttempt) + mkdirScript(workspace),
		cleanup: fmt.Sprintf("printf original > %q", cleanupLog),
	})
	store := testStore(t)
	resource := "https://github.com/org/repo/issues/5"
	if _, err := Create(cfg, store, CreateParams{URL: resource}); err == nil {
		t.Fatal("first setup succeeded")
	}
	sessions, err := store.AllE()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("failed setup sessions = %+v, err %v", sessions, err)
	}
	var name string
	for name = range sessions {
	}
	changed := effectFixtureDoc(taskFixture{
		id: workdirEffectID, scope: "session", setup: "exit 1",
		cleanup: fmt.Sprintf("printf changed > %q", cleanupLog),
	})
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", workdirEffectID+".toml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(cfg, store, CreateParams{URL: resource}); err != nil {
		t.Fatalf("retry with captured effect: %v", err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(cleanupLog); err != nil || string(got) != "original" {
		t.Fatalf("cleanup = %q, err %v, want captured effect", got, err)
	}
}

func TestCreateSharesRevisionUntilDeclarationChanges(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "workspace")
	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	attachWorkdirDir(t, cfg, "wf", workdir)
	store := testStore(t)
	var created []string
	for _, n := range []string{"1", "2"} {
		result, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/" + n})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, result.SessionName)
	}
	first := store.Get(created[0])
	second := store.Get(created[1])
	if first.WorkflowRevision == "" || first.WorkflowRevision != second.WorkflowRevision {
		t.Fatalf("same definition revisions = %q, %q", first.WorkflowRevision, second.WorkflowRevision)
	}
	path := filepath.Join(cfg.BaseDir, "tasks", "runtime.toml")
	if err := os.WriteFile(path, []byte(effectFixtureDoc(taskFixture{id: "runtime", scope: "run", setup: `printf '{}'`})), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/3"})
	if err != nil {
		t.Fatal(err)
	}
	third := store.Get(result.SessionName)
	if third.WorkflowRevision == first.WorkflowRevision {
		t.Fatal("new session kept old revision after declaration changed")
	}
}

func TestCreateRetryKeepsStoredRevisionAfterWorkflowEdit(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "workspace")
	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	attachWorkdirDir(t, cfg, "wf", workdir)
	store := testStore(t)
	resource := "https://github.com/org/repo/issues/5"
	result, err := Create(cfg, store, CreateParams{URL: resource})
	if err != nil {
		t.Fatal(err)
	}
	before := store.Get(result.SessionName)
	addWorkflowFields(t, cfg, "wf", "name = \"Edited after create\"\n")
	if _, err := Create(cfg, store, CreateParams{URL: resource}); err != nil {
		t.Fatal(err)
	}
	if after := store.Get(before.Name); after.WorkflowRevision != before.WorkflowRevision {
		t.Fatalf("retry changed revision from %q to %q", before.WorkflowRevision, after.WorkflowRevision)
	}
}

func TestUpUsesStoredNodeDeclarationAfterEdit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "runtime", scope: "run", setup: fmt.Sprintf("printf old > %q; echo '{}'", marker)}},
		[]nodeFixture{{id: "runtime"}},
	)
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "existing", Workflow: "coding", WorkflowRevision: digest,
		WorkflowRevisionBody: string(body), Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.BaseDir, "tasks", "runtime.toml")
	if err := os.WriteFile(path, []byte(effectFixtureDoc(taskFixture{id: "runtime", scope: "run", setup: fmt.Sprintf("printf new > %q; echo '{}'", marker)})), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "workflows", "coding.toml"), []byte("invalid workflow data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "existing"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "old" {
		t.Fatalf("setup from revision wrote %q, err %v", got, err)
	}
}

func TestUpUsesStoredRevisionWhenDispatchFailsAfterWorkflowFileRemoval(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "runtime", scope: "run", setup: fmt.Sprintf("printf stored > %q; echo '{}'", marker)}},
		[]nodeFixture{{id: "runtime"}},
	)
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "existing", Workflow: "coding", WorkflowRevision: digest,
		WorkflowRevisionBody: string(body), Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "workflows", "coding.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "workflows", "broken.toml"), []byte("invalid workflow data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dispatchResource(cfg, "", "", "existing"); err == nil {
		t.Fatal("dispatch unexpectedly succeeded")
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "existing"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "stored" {
		t.Fatalf("stored setup wrote %q, err %v", got, err)
	}
}

func TestEvaluateHealthUsesStoredProbeAfterEdit(t *testing.T) {
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "runtime", scope: "run", alive: "false"}},
		[]nodeFixture{{id: "runtime"}},
	)
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	seedSessionWithNodes(t, store, "existing", "resource", 1, "coding", map[string]*contract.TaskState{
		"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, TaskID: "runtime"},
	})
	if err := store.Update("existing", func(s *domain.Session) error {
		s.WorkflowRevision = digest
		s.WorkflowRevisionBody = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.BaseDir, "tasks", "runtime.toml")
	if err := os.WriteFile(path, []byte(effectFixtureDoc(taskFixture{id: "runtime", scope: "run", alive: "true"})), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := EvaluateHealth(cfg, store, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy {
		t.Fatalf("health probe followed edited declaration: %+v", report)
	}
}

func TestDestroyUsesStoredCleanupAfterWorkflowFileRemoved(t *testing.T) {
	log := filepath.Join(t.TempDir(), "cleanup.log")
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "original", scope: "run", cleanup: fmt.Sprintf("printf cleaned > %q", log)}},
		[]nodeFixture{{id: "original"}},
	)
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	seedSessionWithNodes(t, store, "session", "resource", 1, "coding", map[string]*contract.TaskState{
		"original": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, TaskID: "original", Seq: 1},
	})
	if err := store.Update("session", func(s *domain.Session) error {
		s.WorkflowRevision = digest
		s.WorkflowRevisionBody = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "workflows", "coding.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "tasks", "original.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "session"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(log); err != nil || string(got) != "cleaned" {
		t.Fatalf("cleanup output = %q, err %v", got, err)
	}
}
