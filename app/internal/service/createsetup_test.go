package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/testpath"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestCreate_WorkflowSetupPath(t *testing.T) {
	store := testStore(t)
	// A subprocess's $(pwd) reports the kernel's resolved cwd (/private/var/...
	// on darwin), so workdir must be resolved the same way before comparing.
	workdir := filepath.Join(testpath.Real(t, t.TempDir()), "wd")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo "{\"cwd\":\"$(pwd)\"}"`}},
		[]nodeFixture{{id: "probe"}})
	attachWorkdirDir(t, cfg, "wf", workdir)

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/5"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	s := store.Get("org/repo-5")
	if s == nil {
		t.Fatal("session not persisted")
	}
	dir := s.Nodes[workdirEffectID]
	if dir == nil || dir.Status != contract.TaskStatusProduced {
		t.Fatalf("workdir node = %+v, want produced", dir)
	}
	if got, err := Workdir(cfg, store, s.Name); err != nil || got != workdir {
		t.Errorf("Workdir = %q, %v, want %q (read from the producer's output)", got, err, workdir)
	}
	// Session nodes ran in the directory the workflow's workdir names.
	probe := s.Nodes["probe"]
	if probe == nil || probe.Status != contract.TaskStatusProduced {
		t.Fatalf("probe = %+v", probe)
	}
	if got, _ := probe.Outputs["cwd"].(string); got != workdir {
		t.Errorf("task cwd = %q, want workdir %q", got, workdir)
	}
	if probe.Directory != workdir {
		t.Errorf("recorded directory = %q, want %q", probe.Directory, workdir)
	}
	if len(s.WorkflowRevision) != 64 {
		t.Fatalf("revision digest = %q, want SHA-256 hex", s.WorkflowRevision)
	}
	entries, err := List(cfg, store)
	if err != nil || len(entries) != 1 || entries[0].Revision != s.WorkflowRevision[:8] {
		t.Fatalf("list revision = %+v, err %v", entries, err)
	}
	status, err := Status(cfg, store, s.Name)
	if err != nil || status.Identity.Revision != s.WorkflowRevision[:8] {
		t.Fatalf("status revision = %+v, err %v", status, err)
	}
}

func TestCreate_RecordsParentSession(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "orchestrator", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `jq -nc --arg parent "{{.ParentSession}}" '{parent:$parent}'`}},
		[]nodeFixture{{id: "probe"}})
	attachWorkdirDir(t, cfg, "wf", workdir)

	if _, err := Create(cfg, store, CreateParams{
		URL:           "https://github.com/org/repo/issues/6",
		ParentSession: "orchestrator",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	child := store.Get("org/repo-6")
	if child == nil {
		t.Fatal("child session not persisted")
	}
	if child.ParentSession != "orchestrator" {
		t.Fatalf("ParentSession = %q, want orchestrator", child.ParentSession)
	}
	if got := child.Nodes["probe"].Outputs["parent"]; got != "orchestrator" {
		t.Fatalf("rendered ParentSession = %v, want orchestrator", got)
	}
	parent := store.Get("orchestrator")
	if parent == nil {
		t.Fatal("parent session missing")
	}
	if got, want := parent.Children, []string{"org/repo-6"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("parent.Children = %v, want %v", got, want)
	}
}

// The initial_task dispatcher is a session node whose setup shells out to a
// nested `plect task setup … --name initial` that writes its instance
// straight to the store. Create's session-tasks persist must overlay
// (mergeTasks), not blind-Put, or that nested write is clobbered. This
// stands in for the real dispatcher with the nested-write test helper
// (nestedwrite_helper_test.go) so the merge is exercised through the real
// store API without a plect binary.
func TestCreate_SessionNodeNestedWriteSurvives(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	t.Setenv("PLECT_SERVICE_NESTED_WRITE_HELPER", "1")

	// dispatcher writes a sibling "initial" key straight to the store
	// (mimicking the nested `plect task setup`), then produces normally.
	dispatcher := nestedWriteCommand(t, store.Dir(), "org/repo-11", nestedWritePatch{
		Tasks: map[string]*contract.TaskState{
			"initial": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "work", Name: "initial", Outputs: map[string]any{"instruction": "start work"}},
		},
	}) + `
echo '{}'`
	cfg := writeWorkflowFixture(t, "claude",
		[]taskFixture{{id: "dispatcher", scope: "session", setup: dispatcher}},
		[]nodeFixture{{id: "dispatcher"}})
	attachWorkdirDir(t, cfg, "claude", workdir)

	url := "https://github.com/org/repo/issues/11"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	session := store.Get("org/repo-11")
	if session == nil {
		t.Fatal("session not persisted")
	}
	// The dispatcher node itself produced…
	if st := session.Nodes["dispatcher"]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("dispatcher node = %+v, want produced", st)
	}
	// …and its nested write survived the session-tasks persist.
	got := session.Tasks["initial"]
	if got == nil {
		t.Fatal("nested-written 'initial' instance was clobbered by the create persist")
	}
	if got.Name != "initial" || got.TaskID != "work" || got.Outputs["instruction"] != "start work" {
		t.Fatalf("initial instance mismatch: %+v", got)
	}
}

// Binding-implies-delivery applies at session creation too, not only to a
// dynamic task setup: the session's own resource is wired through the same
// subscribeIfWired call TaskSetup makes for an explicit --resource.
func TestCreate_WorkflowSetupRecordsLifecycleCreated(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "wf", workdir)

	url := "https://github.com/org/repo/issues/7"
	countCreated := func() int {
		evs, _, _, err := EventList(cfg, store, url, 0, event.Filter{Types: []string{"lifecycle.created"}})
		if err != nil {
			t.Fatalf("event list: %v", err)
		}
		return len(evs)
	}

	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n := countCreated(); n != 1 {
		t.Fatalf("lifecycle.created after create = %d, want 1", n)
	}

	// Re-run on the now-existing session must not append a second created.
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if n := countCreated(); n != 1 {
		t.Fatalf("lifecycle.created after re-run = %d, want 1 (no duplicate)", n)
	}
}

func TestCreate_WorkflowSetupFailureLeavesRetryableState(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	gate := filepath.Join(t.TempDir(), "gate")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	// Fails until the gate file exists — simulates a transient outage.
	attachWorkdir(t, cfg, "wf", taskFixture{setup: fmt.Sprintf("[ -f %s ] || { echo \"transient\" >&2; exit 1; }\n", gate) + mkdirScript(workdir)})

	url := "https://github.com/org/repo/issues/6"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err == nil {
		t.Fatal("expected first create to fail")
	}
	s := store.Get("org/repo-6")
	if s == nil {
		t.Fatal("failed setup must still leave an inspectable state entry")
	}
	if st := s.Nodes[workdirEffectID]; st == nil || st.Status != contract.TaskStatusFailed {
		t.Fatalf("workdir node = %+v, want failed", st)
	}

	if err := os.WriteFile(gate, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	s = store.Get("org/repo-6")
	if st := s.Nodes[workdirEffectID]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("workdir node after retry = %+v, want produced", st)
	}

	// The first attempt left an inspectable state entry but failed before
	// recording lifecycle.created; the retry is the first *success*, so created
	// must be recorded exactly once (not skipped because the entry pre-existed).
	evs, _, _, err := EventList(cfg, store, url, 0, event.Filter{Types: []string{"lifecycle.created"}})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("lifecycle.created after retry-success = %d, want 1", len(evs))
	}
}

// A session torn down must drop its own resource's event-delivery
// registration, through the same locked, durable-retry unsubscribe path
// TaskCleanup runs for a dynamic instance's own bound resource.
func TestDestroy_RunsSessionEffectCleanup(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	marker := filepath.Join(t.TempDir(), "released")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdir(t, cfg, "wf", taskFixture{
		setup:   mkdirScript(workdir),
		cleanup: fmt.Sprintf(`rm -rf '{{.Self "workspace_dir"}}' && touch %q`, marker),
	})

	url := "https://github.com/org/repo/issues/7"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "workflows", "wf.toml")); err != nil {
		t.Fatal(err)
	}

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-7"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("effect cleanup did not run")
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("workdir should have been removed by its producer's cleanup: %v", err)
	}
	if store.Get("org/repo-7") != nil {
		t.Error("state entry should be deleted")
	}
}

func TestDestroy_EffectCleanupFailureBlocksWithoutForce(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdir(t, cfg, "wf", taskFixture{setup: mkdirScript(workdir), cleanup: "exit 9"})

	url := "https://github.com/org/repo/issues/8"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-8"})
	if err == nil {
		t.Fatal("expected destroy to fail-fast on cleanup error")
	}
	if !strings.Contains(err.Error(), workdirEffectID) {
		t.Errorf("error should name the failing node: %v", err)
	}
	if store.Get("org/repo-8") == nil {
		t.Error("state entry must survive a blocked destroy")
	}

	result, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-8", Force: true})
	if err != nil {
		t.Fatalf("forced destroy: %v", err)
	}
	if len(result.CleanupWarnings) == 0 {
		t.Error("forced destroy should surface the cleanup failure as a warning")
	}
	if store.Get("org/repo-8") != nil {
		t.Error("forced destroy should delete the state entry")
	}
}

func TestUp_RecoversFailedWorkflowSetup(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	gate := filepath.Join(t.TempDir(), "gate")

	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdir(t, cfg, "wf", taskFixture{setup: fmt.Sprintf("[ -f %s ] || { echo transient >&2; exit 1; }\n", gate) + mkdirScript(workdir)})

	url := "https://github.com/org/repo/issues/9"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err == nil {
		t.Fatal("expected first create to fail")
	}
	if err := os.WriteFile(gate, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// docker compose up-style: Up with a URL must auto-recover the failed setup.
	if _, err := Up(cfg, store, UpParams{Identifier: url}); err != nil {
		t.Fatalf("Up should recover the partial create: %v", err)
	}
	s := store.Get("org/repo-9")
	if st := s.Nodes[workdirEffectID]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("workdir node = %+v, want produced after Up recovery", st)
	}
}
