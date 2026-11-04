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
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	plan, err := buildPlanForSession(cfg, "", session)
	if err != nil || len(plan.Run) != 1 || plan.Run[0].NodeID != "original" {
		t.Fatalf("stored plan = %+v, err %v", plan, err)
	}
}

func TestBuildPlanForSessionKeepsExternalSchemaAfterFileRemoved(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	plan, err := buildPlanForSession(cfg, "", session)
	if err != nil || plan == nil || len(plan.Run) != 1 {
		t.Fatalf("stored plan with external schema = %+v, err %v", plan, err)
	}
}

func TestBackfillWorkflowRevisionsPinsExistingSessionAtMigration(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	plan, err := buildPlanForSession(cfg, "", session)
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
		Nodes: map[string]*contract.TaskState{contract.WorkflowPseudoNodeID: {Status: contract.TaskStatusFailed, Scope: contract.TaskScopeSession}},
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

func TestCreateSharesRevisionUntilDeclarationChanges(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "workspace")
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	writeSetupWorkflow(t, cfg, "wf", providerRunningScript("wf", fmt.Sprintf("mkdir -p %q; printf '{\"workspace_dir\":\"%s\"}'", workdir, workdir)))
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

func TestCreateRetryUsesStoredProviderInputsAfterWorkflowEdit(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "workspace")
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	writeSetupWorkflow(t, cfg, "wf", providerCreatingWorkspace("wf", workdir))
	store := testStore(t)
	resource := "https://github.com/org/repo/issues/5"
	result, err := Create(cfg, store, CreateParams{URL: resource})
	if err != nil {
		t.Fatal(err)
	}
	before := store.Get(result.SessionName)
	addWorkflowFields(t, cfg, "wf", "workspace_provider_inputs = { unexpected = \"new\" }\n")
	if _, err := Create(cfg, store, CreateParams{URL: resource}); err != nil {
		t.Fatal(err)
	}
	if after := store.Get(before.Name); after.WorkflowRevision != before.WorkflowRevision {
		t.Fatalf("retry changed revision from %q to %q", before.WorkflowRevision, after.WorkflowRevision)
	}
}

func TestUpUsesStoredNodeDeclarationAfterEdit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	if _, _, err := dispatchResource(cfg, "", "existing"); err == nil {
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
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
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
