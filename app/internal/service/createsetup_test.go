package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// writeSetupWorkflow gives the fixture workflow a resource provider: the
// extra TOML (setup/cleanup/match/name/outputs_schema — flat provider keys)
// lands in providers/<wfID>.toml and the workflow gains `provider = "<wfID>"`.
// Prepend, not append — top-level keys after a [[nodes]] table would be
// parsed as part of that table.
func writeSetupWorkflow(t *testing.T, cfg *config.Config, wfID, extra string) {
	t.Helper()
	providersDir := filepath.Join(cfg.BaseDir, "providers")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providersDir, wfID+".toml"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", wfID+".toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	header := "provider = \"" + wfID + "\"\n"
	if err := os.WriteFile(wfPath, append([]byte(header), existing...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreate_WorkflowSetupPath(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo "{\"cwd\":\"$(pwd)\"}"`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s","branch":"issue/5","title":"T"}'
'''
`, workdir, workdir))

	result, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/5"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.WorktreePath != workdir {
		t.Errorf("WorktreePath = %q, want %q (mirrored from workdir output)", result.WorktreePath, workdir)
	}
	if result.Branch != "issue/5" {
		t.Errorf("Branch = %q, want issue/5", result.Branch)
	}

	s := store.Get("org/repo-5")
	if s == nil {
		t.Fatal("session not persisted")
	}
	wfState := s.Tasks[contract.WorkflowPseudoNodeID]
	if wfState == nil || wfState.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node = %+v, want produced", wfState)
	}
	if wfState.Outputs["title"] != "T" {
		t.Errorf("free-form outputs should persist: %v", wfState.Outputs)
	}
	// Session tasks ran with the new workdir as cwd.
	probe := s.Tasks["probe"]
	if probe == nil || probe.Status != contract.TaskStatusProduced {
		t.Fatalf("probe = %+v", probe)
	}
	if got, _ := probe.Outputs["cwd"].(string); got != workdir {
		t.Errorf("task cwd = %q, want workdir %q", got, workdir)
	}
}

func TestCreate_RecordsParentSession(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "orchestrator", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo "{\"parent\":\"{{.ParentSession}}\"}"`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir))

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
	if got := child.Tasks["probe"].Outputs["parent"]; got != "orchestrator" {
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
// nested `tws task setup … --name initial` that writes its instance straight
// to state.json. Create's session-tasks persist must overlay (mergeTasks),
// not blind-Put, or that nested write is clobbered. This stands in for the real
// dispatcher with a jq writer so the merge is exercised without a tws binary.
func TestCreate_SessionNodeNestedWriteSurvives(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	t.Setenv("SP", filepath.Join(store.Dir(), "state.json"))

	// dispatcher writes a sibling "initial" key to disk (mimicking the nested
	// `tws task setup`), then produces normally.
	dispatcher := `jq '.sessions["org/repo-11"].tasks.initial={"scope":"session","status":"produced","dynamic":true,"task_id":"work","name":"initial","outputs":{"instruction":"start work"}}' "$SP" > "$SP.tmp" && mv "$SP.tmp" "$SP"
echo '{}'`
	cfg := writeWorkflowFixture(t, t.TempDir(), "claude",
		[]taskFixture{{id: "dispatcher", scope: "session", setup: dispatcher}},
		[]nodeFixture{{id: "dispatcher"}})
	writeSetupWorkflow(t, cfg, "claude", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir))

	url := "https://github.com/org/repo/issues/11"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	session := store.Get("org/repo-11")
	if session == nil {
		t.Fatal("session not persisted")
	}
	// The dispatcher node itself produced…
	if st := session.Tasks["dispatcher"]; st == nil || st.Status != contract.TaskStatusProduced {
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

// Regression: the workflow-setup create path must record
// lifecycle.created for a new session (the legacy path did, this one didn't),
// and a recovery/re-run must not append a duplicate.
func TestCreate_WorkflowSetupRecordsLifecycleCreated(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir))

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

// Stream is a create-time identity: the first create sets it, and an idempotent
// retry with a different --stream must NOT overwrite it (that would split the
// session's later events into another cross-session stream). Mirrors the Up
// contract documented on UpParams.StreamID.
func TestCreate_StreamIDIsWriteOnce(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir))

	url := "https://github.com/org/repo/issues/8"
	if _, err := Create(cfg, store, CreateParams{URL: url, StreamID: "stream-first"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := store.Get("org/repo-8").StreamID; got != "stream-first" {
		t.Fatalf("StreamID after create = %q, want stream-first", got)
	}

	// Idempotent retry carrying a different stream must keep the original.
	if _, err := Create(cfg, store, CreateParams{URL: url, StreamID: "stream-second"}); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if got := store.Get("org/repo-8").StreamID; got != "stream-first" {
		t.Fatalf("StreamID after retry = %q, want stream-first (write-once identity)", got)
	}
}

// With no --stream, core adopts a stream_id from the provider's setup
// output as the session's own StreamID — the single source for the
// orchestrator route. The claude task then exports it from {{.StreamID}}.
func TestCreate_AdoptsProviderStreamID(t *testing.T) {
	t.Setenv("TWS_STREAM_ID", "") // no inherited stream; the provider supplies it
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s","stream_id":"provider-stream"}'
'''
`, workdir, workdir))

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/9"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := store.Get("org/repo-9").StreamID; got != "provider-stream" {
		t.Fatalf("StreamID = %q, want provider-stream (adopted from setup output)", got)
	}
}

// The explicit --stream (and the inherited TWS_STREAM_ID env behind it)
// is the session's identity and must win over the provider's stream_id output,
// so a dispatched child keeps the parent's stream.
func TestCreate_ExplicitStreamWinsOverProviderOutput(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s","stream_id":"provider-stream"}'
'''
`, workdir, workdir))

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/10", StreamID: "explicit-stream"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := store.Get("org/repo-10").StreamID; got != "explicit-stream" {
		t.Fatalf("StreamID = %q, want explicit-stream (flag wins over provider output)", got)
	}
}

func TestCreate_WorkflowSetupFailureLeavesRetryableState(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	gate := filepath.Join(t.TempDir(), "gate")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	// Fails until the gate file exists — simulates a transient gh outage.
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
[ -f %s ] || { echo "transient" >&2; exit 1; }
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, gate, workdir, workdir))

	url := "https://github.com/org/repo/issues/6"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err == nil {
		t.Fatal("expected first create to fail")
	}
	s := store.Get("org/repo-6")
	if s == nil {
		t.Fatal("failed setup must still leave an inspectable state entry")
	}
	if st := s.Tasks[contract.WorkflowPseudoNodeID]; st == nil || st.Status != contract.TaskStatusFailed {
		t.Fatalf("pseudo-node = %+v, want failed", st)
	}

	if err := os.WriteFile(gate, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	s = store.Get("org/repo-6")
	if st := s.Tasks[contract.WorkflowPseudoNodeID]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node after retry = %+v, want produced", st)
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

func TestDestroy_RunsWorkflowCleanup(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	marker := filepath.Join(t.TempDir(), "released")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
cleanup = '''
rm -rf "{{.Self.workdir}}"
touch %s
'''
`, workdir, workdir, marker))

	url := "https://github.com/org/repo/issues/7"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-7"})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("workflow cleanup did not run")
	}
	if !result.RemovedWorktree {
		t.Error("workdir was removed by cleanup; result should report it")
	}
	if store.Get("org/repo-7") != nil {
		t.Error("state entry should be deleted")
	}
}

func TestDestroy_WorkflowCleanupFailureBlocksWithoutForce(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
cleanup = "exit 9"
`, workdir, workdir))

	url := "https://github.com/org/repo/issues/8"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-8"})
	if err == nil {
		t.Fatal("expected destroy to fail-fast on cleanup error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should hint at --force: %v", err)
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

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
[ -f %s ] || { echo "transient" >&2; exit 1; }
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, gate, workdir, workdir))

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
	if st := s.Tasks[contract.WorkflowPseudoNodeID]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node = %+v, want produced after Up recovery", st)
	}
}
