package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cradel-dev/cradel/app/internal/eventlog"
	"github.com/cradel-dev/cradel/app/internal/task"
	"github.com/cradel-dev/cradel/contracts/event"
	contract "github.com/cradel-dev/cradel/contracts/state"
)

func TestTaskSetup_AppendsInstructionEvent(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{"instruction":"resolve the issue"}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID: "work", SessionName: "o/r-1", Resource: "https://github.com/o/r/issues/1",
	}); err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("o/r-1", 0, event.Filter{Types: []string{event.TypeInstruction}})
	if err != nil || len(evs) != 1 {
		t.Fatalf("want one sennit.instruction event, got %d (err=%v)", len(evs), err)
	}
	ev := evs[0]
	if ev.Body != "resolve the issue" || ev.Direction != event.Inbound || ev.Source != event.SourceSennit {
		t.Errorf("instruction event = %+v", ev)
	}
	if ev.Metadata["task"] != "work#1" || ev.Metadata["resource"] != "https://github.com/o/r/issues/1" {
		t.Errorf("instruction metadata = %+v", ev.Metadata)
	}
}

func TestTaskSetup_NoInstructionEventWithoutOutput(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "review", scope: "session", setup: `echo '{"ready":"yes"}'`}},
		[]nodeFixture{{id: "review"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1"}); err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	evs, _, _, _ := eventlog.NewStore(store.Dir()).List("o/r-1", 0, event.Filter{Types: []string{event.TypeInstruction}})
	if len(evs) != 0 {
		t.Fatalf("a task with no instruction output must append no event, got %d", len(evs))
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// A concurrent down that cleans an instance during its (unlocked) setup window
// must not be resurrected to produced by the Phase-3 merge.
func TestTaskSetup_NoResurrectAfterConcurrentDown(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	gate := filepath.Join(t.TempDir(), "gate")
	blockingSetup := `while [ ! -e ` + gate + ` ]; do sleep 0.01; done; echo '{}'`
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "storybook", scope: "run", setup: blockingSetup, cleanup: "true"},
		},
		[]nodeFixture{{id: "tmux"}},
	)
	store := testStore(t)
	// Run scope is up (a live run-scoped static node), so storybook may instantiate.
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{
		"tmux": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 4, Outputs: map[string]any{}},
	})

	var runErr error
	done := make(chan struct{})
	go func() {
		_, runErr = TaskSetup(cfg, store, TaskSetupParams{TaskID: "storybook", SessionName: "o/r-1"})
		close(done)
	}()

	// Wait until the reservation lands, then down it while setup is still blocked.
	waitUntil(t, func() bool {
		s := store.Get("o/r-1")
		return s != nil && s.Tasks["storybook#1"] != nil
	})
	if _, err := Down(cfg, store, DownParams{Identifier: "o/r-1"}); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Unblock setup so TaskSetup proceeds to its merge.
	if err := os.WriteFile(gate, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	<-done

	if runErr == nil {
		t.Error("TaskSetup must error when its instance was torn down mid-setup")
	}
	st := store.Get("o/r-1").Tasks["storybook#1"]
	if st == nil || st.Status != contract.TaskStatusCleaned {
		t.Errorf("instance must stay cleaned, not resurrected to produced: %+v", st)
	}
}

// orderObserver records the ids of tasks that successfully cleaned, in order.
type orderObserver struct{ cleaned []string }

func (o *orderObserver) OnStart(string, string)        {}
func (o *orderObserver) OnSkip(string, string, string) {}
func (o *orderObserver) OnSuccess(_, id string, _ time.Duration, _ []byte) {
	o.cleaned = append(o.cleaned, id)
}
func (o *orderObserver) OnFailure(string, string, time.Duration, error, []byte) {}

var _ task.Observer = (*orderObserver)(nil)

func TestTaskSetup_SessionScopedWhileDown(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "review", scope: "session", setup: `echo '{"ready":"yes"}'`}},
		[]nodeFixture{{id: "review"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1", Resource: "pr-1"})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	// Name-less: key is "<task>#<n>" (per-task sequential), decoupled from
	// the resource (which never appears in the key).
	if res.Instance != "review#1" {
		t.Errorf("instance = %q, want review#1", res.Instance)
	}
	st := store.Get("o/r-1").Tasks[res.Instance]
	if st == nil || st.Status != contract.TaskStatusProduced || !st.Dynamic {
		t.Fatalf("expected produced dynamic instance, got %+v", st)
	}
	if st.Resource != "pr-1" || st.TaskID != "review" {
		t.Errorf("resource/task_id = %q/%q", st.Resource, st.TaskID)
	}
	if st.Name != "" {
		t.Errorf("name = %q, want empty for the numbered form", st.Name)
	}
}

func TestTaskSetup_AppendsExtraDoneWhen(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{"checks_status":"SUCCESS","revision":"sha1"}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	res, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID:            "work",
		SessionName:       "o/r-1",
		ExtraDoneWhenJSON: `{"all":[{"judge":"Codex review approved","id":"codex-review","reviewer_workflow":"codex","reject_self":true}]}`,
	})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	st := store.Get("o/r-1").Tasks[res.Instance]
	if len(st.ExtraDoneWhen) == 0 || !strings.Contains(string(st.ExtraDoneWhen), "codex-review") {
		t.Fatalf("ExtraDoneWhen = %s, want persisted codex-review judge", st.ExtraDoneWhen)
	}
}

func TestTaskSetup_RunScopedRejectedWhenDown(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "storybook", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "storybook"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	_, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "storybook", SessionName: "o/r-1"})
	if err == nil {
		t.Fatal("expected error: run-scoped task while run scope down")
	}
	if svcErr, ok := err.(*Error); !ok || svcErr.Code != ErrInvalidInput {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

func TestTaskSetup_RunScopedAllowedWhenUp(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "storybook", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "storybook"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{
		"tmux": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 4},
	})

	res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "storybook", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	if st := store.Get("o/r-1").Tasks[res.Instance]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("expected produced storybook, got %+v", st)
	}
}

// Per-task sequential numbering: each run increments (1,2,3); different
// tasks number independently; numbers are monotonic across a destroyed
// instance (no reuse).
func TestTaskSetup_SequentialPerTask(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "true"},
			{id: "investigate", scope: "session", setup: `echo '{}'`},
		},
		[]nodeFixture{{id: "review"}, {id: "investigate"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	mustRun := func(taskID string) string {
		t.Helper()
		res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: taskID, SessionName: "o/r-1"})
		if err != nil {
			t.Fatalf("run %s: %v", taskID, err)
		}
		return res.Instance
	}

	if got := mustRun("review"); got != "review#1" {
		t.Errorf("first review = %q, want review#1", got)
	}
	if got := mustRun("review"); got != "review#2" {
		t.Errorf("second review = %q, want review#2", got)
	}
	// Different task numbers independently.
	if got := mustRun("investigate"); got != "investigate#1" {
		t.Errorf("first investigate = %q, want investigate#1", got)
	}
	if got := mustRun("review"); got != "review#3" {
		t.Errorf("third review = %q, want review#3", got)
	}

	// A cleaned instance still occupies its number — re-running does not reuse 1.
	s := store.Get("o/r-1")
	s.Tasks["review#1"].Status = contract.TaskStatusCleaned
	if err := store.Put(s); err != nil {
		t.Fatal(err)
	}
	if got := mustRun("review"); got != "review#4" {
		t.Errorf("after cleaning review#1, next = %q, want review#4 (monotonic)", got)
	}
}

// A --name instance keys on the name alone (no <task># prefix), so it is a
// session-global singleton addressable as that name.
func TestTaskSetup_NamedInstanceKeysOnName(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{"instruction":"x"}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	if res.Instance != "initial" {
		t.Errorf("instance = %q, want initial (name-only key)", res.Instance)
	}
	st := store.Get("o/r-1").Tasks["initial"]
	if st == nil || st.Name != "initial" || st.TaskID != "work" || !st.Dynamic {
		t.Fatalf("named instance state = %+v", st)
	}
}

// A second setup of an existing name is a collision error (no auto-recover);
// the name-less numbered form never collides.
func TestTaskSetup_NameCollisionErrors(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"}); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	// Same name, even a different task, collides — name is session-global.
	_, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "initial"})
	if err == nil {
		t.Fatal("expected collision error on duplicate --name")
	}
	if svcErr, ok := err.(*Error); !ok || svcErr.Code != ErrInvalidInput {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

// A --name carrying a '#' (or otherwise not an identifier) is rejected before
// any state mutation, so it can't masquerade as a numbered key.
func TestTaskSetup_RejectsInvalidName(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	_, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Name: "work#9"})
	if err == nil {
		t.Fatal("expected error for --name with '#'")
	}
	if store.Get("o/r-1").Tasks["work#9"] != nil {
		t.Error("invalid name must not leave a reservation")
	}
}

const intentSchema = `
[inputs_schema]
type = "object"
required = ["intent"]
[inputs_schema.properties.intent]
type = "string"
`

func TestTaskSetup_InputBindingPrecedence(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `printf '{"intent":"%s"}' '{{.Inputs.intent}}'`, extra: intentSchema}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)

	// --input wins.
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})
	res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Resource: "a", Inputs: map[string]string{"intent": "work"}})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	if res.Outputs["intent"] != "work" {
		t.Errorf("--input not bound: outputs = %v", res.Outputs)
	}

	// Falls back to session inputs when no --input given.
	s := store.Get("o/r-1")
	s.Inputs = map[string]any{"intent": "fromsession"}
	if err := store.Put(s); err != nil {
		t.Fatal(err)
	}
	res2, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Resource: "b"})
	if err != nil {
		t.Fatalf("TaskSetup (session fallback): %v", err)
	}
	if res2.Outputs["intent"] != "fromsession" {
		t.Errorf("session input not bound: outputs = %v", res2.Outputs)
	}
}

func TestTaskSetup_RejectsUndeclaredInput(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, extra: intentSchema}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	_, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "o/r-1", Inputs: map[string]string{"intent": "work", "bogus": "1"}})
	if err == nil {
		t.Fatal("expected error for undeclared --input")
	}
	if svcErr, ok := err.(*Error); !ok || svcErr.Code != ErrInvalidInput {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRefreshInstanceOutputs_UsesInstanceResource(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "claude",
		[]taskFixture{{id: "review", scope: "session", setup: `echo '{}'`, extra: `
[[outputs]]
name = "seen_resource"
script = "printf '%s' '{{.ResourceID}}'"
`}},
		[]nodeFixture{{id: "review"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "claude", map[string]*contract.TaskState{
		"review#1": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Dynamic: true, TaskID: "review", Resource: "pr-1", Outputs: map[string]any{}},
		"review#2": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Dynamic: true, TaskID: "review", Resource: "pr-2", Outputs: map[string]any{}},
	})

	results, err := RefreshInstanceOutputs(cfg, store, "o/r-1", "review#2")
	if err != nil {
		t.Fatalf("RefreshInstanceOutputs: %v", err)
	}
	if len(results) != 1 || !results[0].Fetched || results[0].Value != "pr-2" {
		t.Fatalf("refresh results = %+v, want pr-2 fetched", results)
	}
	s := store.Get("o/r-1")
	if got := s.Tasks["review#2"].Outputs["seen_resource"]; got != "pr-2" {
		t.Errorf("review#2 seen_resource = %v, want pr-2", got)
	}
	if _, ok := s.Tasks["review#1"].Outputs["seen_resource"]; ok {
		t.Errorf("review#1 should not be updated when refreshing review#2: %+v", s.Tasks["review#1"].Outputs)
	}
}

// Down reclaims a run-scoped dynamic instance ahead of the static run node it
// was instantiated after; a session-scoped dynamic instance survives.
func TestDown_ReclaimsDynamicRunInstance(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "storybook", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "tmux"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{
		"tmux": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 4, Outputs: map[string]any{}},
	})

	reviewRes, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1", Resource: "pr-1"})
	if err != nil {
		t.Fatalf("instantiate review: %v", err)
	}
	storyRes, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "storybook", SessionName: "o/r-1"})
	if err != nil {
		t.Fatalf("instantiate storybook: %v", err)
	}

	obs := &orderObserver{}
	if _, err := Down(cfg, store, DownParams{Identifier: "o/r-1", Observer: obs}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	s := store.Get("o/r-1")
	if s.Tasks[storyRes.Instance].Status != contract.TaskStatusCleaned {
		t.Errorf("storybook should be cleaned: %+v", s.Tasks[storyRes.Instance])
	}
	if s.Tasks["tmux"].Status != contract.TaskStatusCleaned {
		t.Errorf("tmux should be cleaned: %+v", s.Tasks["tmux"])
	}
	if s.Tasks[reviewRes.Instance].Status != contract.TaskStatusProduced {
		t.Errorf("session-scoped review should survive down: %+v", s.Tasks[reviewRes.Instance])
	}
	// Reverse-instantiation: storybook (dynamic, newer) before tmux (static).
	if idx(obs.cleaned, storyRes.Instance) > idx(obs.cleaned, "tmux") {
		t.Errorf("expected storybook cleaned before tmux, got order %v", obs.cleaned)
	}
}

// Teardown must stay resilient when a dynamic instance's task def has drifted
// to invalid config (here: a `requires` naming an output the schema lacks).
// ResolveDefinition would reject it, but teardown only needs the cleanup script,
// so destroy must still run the cleanup and reclaim the session.
func TestDestroy_ResilientToInvalidDefConfig(t *testing.T) {
	marker := t.TempDir() + "/cleaned"
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "touch " + marker,
				extra: "requires = [\"nonexistent_output\"]\n"},
		},
		[]nodeFixture{{id: "review"}},
	)
	store := testStore(t)
	// A produced dynamic instance of the (now-invalid) task.
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{
		"review#1": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "review", Dynamic: true, Seq: 2, Outputs: map[string]any{}},
	})

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "o/r-1"}); err != nil {
		t.Fatalf("Destroy must not be fatal on invalid def config: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("cleanup script should have run despite invalid requires: %v", err)
	}
	if store.Get("o/r-1") != nil {
		t.Error("session should be deleted after destroy")
	}
}

func TestDestroy_ReclaimsDynamicSessionInstance(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "tmux"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1", Resource: "pr-1"}); err != nil {
		t.Fatalf("instantiate review: %v", err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "o/r-1"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if store.Get("o/r-1") != nil {
		t.Error("session should be deleted after destroy")
	}
}

// Concurrent `sennit task run`s against one session must all survive with
// distinct keys (the under-lock reservation prevents two runs allocating the
// same sequential number) and distinct Seq (the merge re-reads under the lock).
func TestTaskSetup_ConcurrentInstancesDistinctSeq(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{{id: "review", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "review"}},
	)
	store := testStore(t)
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{})

	const n = 4
	errs := make([]error, n)
	keys := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "review", SessionName: "o/r-1"})
			errs[i] = err
			if res != nil {
				keys[i] = res.Instance
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("TaskSetup[%d]: %v", i, err)
		}
	}
	s := store.Get("o/r-1")
	seqOwner := map[int]string{}
	keyOwner := map[string]bool{}
	for i, key := range keys {
		if keyOwner[key] {
			t.Errorf("duplicate instance key %q (reservation collision)", key)
		}
		keyOwner[key] = true
		st := s.Tasks[key]
		if st == nil || st.Status != contract.TaskStatusProduced {
			t.Fatalf("instance %d (%s) lost or not produced: %+v", i, key, st)
		}
		if prev, dup := seqOwner[st.Seq]; dup {
			t.Errorf("duplicate seq %d shared by %s and %s", st.Seq, prev, key)
		}
		seqOwner[st.Seq] = key
	}
	if len(keyOwner) != n {
		t.Errorf("expected %d distinct instances, got %d: %v", n, len(keyOwner), keys)
	}
}

// Review finding 2 regression: a static node instantiated after a dynamic instance (higher
// seq) must still be cleaned first. The teardown is one strict seq-descending
// pass, not "all dynamic first, then all static".
func TestDestroy_StrictReverseSeqAcrossStaticDynamic(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "coding",
		[]taskFixture{
			{id: "tmux", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "claude", scope: "run", setup: `echo '{}'`, cleanup: "true"},
			{id: "review", scope: "session", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{{id: "tmux"}, {id: "claude"}},
	)
	store := testStore(t)
	// Seq inversion: the dynamic review (seq 3) sits between tmux (2) and a
	// later-stamped claude (4) — e.g. a re-`up` re-stamped claude after review
	// was instantiated.
	seedSession(t, store, "o/r-1", "o/r", 1, "coding", map[string]*contract.TaskState{
		"tmux":        {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 2, Outputs: map[string]any{}},
		"review#pr-1": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Seq: 3, Dynamic: true, TaskID: "review", Resource: "pr-1", Outputs: map[string]any{}},
		"claude":      {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 4, Outputs: map[string]any{}},
	})

	obs := &orderObserver{}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "o/r-1", Observer: obs}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// Strict reverse-instantiation: claude(4) → review#pr-1(3) → tmux(2).
	if idx(obs.cleaned, "claude") > idx(obs.cleaned, "review#pr-1") || idx(obs.cleaned, "review#pr-1") > idx(obs.cleaned, "tmux") {
		t.Errorf("expected claude → review#pr-1 → tmux, got %v", obs.cleaned)
	}
}

func idx(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
