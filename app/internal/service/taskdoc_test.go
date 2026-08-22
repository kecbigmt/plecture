package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// reviewDocument is the acceptance case the completion surface exists for: a
// verdict recorded into the instance, compared against the resource's live
// revision, with no key of its own to hang on.
const reviewDocument = `+++
[review]
kind              = "task"
description       = "Review a resource and record a verdict"
resource_observer = "issue_pr"

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
]
+++
Review {{ resource.id }} and record a verdict against its current revision.
`

// observeScript is the observer's `observe` action: it reports a fixed
// observation so a test can assert what the completion surface did with it.
func observerDocument(state map[string]string) string {
	pairs := make([]string, 0, len(state))
	for k, v := range state {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	var props strings.Builder
	for k := range state {
		fmt.Fprintf(&props, "%s = { type = \"string\" }\n", k)
	}
	return fmt.Sprintf(`
[issue_pr]
kind  = "resource_observer"
match = '^https://'

[issue_pr.observe]
type   = "shell"
script = '''
jq -nc --args '$ARGS.positional | map(split("=")) | map({(.[0]): .[1]}) | add' %s
'''

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
%s
`, strings.Join(pairs, " "), props.String())
}

// writeTaskDocumentFixture writes one observer, one empty workflow, and the
// given task documents into a config layer.
func writeTaskDocumentFixture(t *testing.T, workdirsRoot, wfID string, observerState map[string]string, documents ...string) *config.Config {
	t.Helper()
	baseDir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(baseDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("resources", "issue_pr.toml"), observerDocument(observerState))
	write(filepath.Join("workflows", wfID+".toml"), "[[nodes]]\nid = \"noop\"\n")
	for i, doc := range documents {
		write(filepath.Join("tasks", fmt.Sprintf("doc%d.md", i)), doc)
	}
	return &config.Config{WorkspaceDirsRoot: workdirsRoot, BaseDir: baseDir}
}

func TestSetTaskState_RecordsDeclaredKey(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", map[string]string{"resource_kind": "pull", "revision": "sha2"}, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", map[string]*contract.TaskState{
		"review#1": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "review",
			Status:   contract.TaskStatusProduced,
			Resource: "https://example.test/pull/1",
			SetupAt:  time.Now(),
		},
	})

	result, err := SetTaskState(cfg, store, SetTaskStateParams{
		Identifier: "org/repo-1",
		Instance:   "review#1",
		State:      map[string]any{"verdict_revision": "sha2"},
	})
	if err != nil {
		t.Fatalf("SetTaskState: %v", err)
	}
	if result.Instance != "review#1" || len(result.Keys) != 1 || result.Keys[0] != "verdict_revision" {
		t.Errorf("result = %+v", result)
	}
	got := store.Get("org/repo-1").Tasks["review#1"].State
	if got["verdict_revision"] != "sha2" {
		t.Errorf("state = %v, want verdict_revision=sha2", got)
	}
}

func TestSetTaskState_UndeclaredKeyRejected(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", map[string]string{"resource_kind": "pull", "revision": "sha2"}, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", map[string]*contract.TaskState{
		"review#1": {Scope: contract.TaskScopeSession, TaskID: "review", Status: contract.TaskStatusProduced, SetupAt: time.Now()},
	})

	_, err := SetTaskState(cfg, store, SetTaskStateParams{
		Identifier: "org/repo-1",
		Instance:   "review#1",
		State:      map[string]any{"nope": "x"},
	})
	if err == nil {
		t.Fatal("expected a rejection for a key the document's state_schema does not declare")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// An instance backed by an effect declaration holds no state of its own:
// there is no state_schema to validate a write against, so the write is
// refused rather than landing somewhere nothing reads.
func TestSetTaskState_NonDocumentInstanceRejected(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "watch", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "watch"}})
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", map[string]*contract.TaskState{
		"watch": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, SetupAt: time.Now()},
	})

	_, err := SetTaskState(cfg, store, SetTaskStateParams{
		Identifier: "org/repo-1",
		Instance:   "watch",
		State:      map[string]any{"verdict_revision": "sha2"},
	})
	if err == nil {
		t.Fatal("expected a rejection for an instance no task document declares")
	}
}

func TestSetTaskState_CleanedInstanceRejected(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", map[string]string{"resource_kind": "pull", "revision": "sha2"}, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", map[string]*contract.TaskState{
		"review#1": {Scope: contract.TaskScopeSession, TaskID: "review", Status: contract.TaskStatusCleaned, SetupAt: time.Now()},
	})

	_, err := SetTaskState(cfg, store, SetTaskStateParams{
		Identifier: "org/repo-1",
		Instance:   "review#1",
		State:      map[string]any{"verdict_revision": "sha2"},
	})
	if err == nil {
		t.Fatal("expected a rejection for an instance that is no longer live")
	}
}

// Instantiation binds the document to a resource, observes it once, and
// records the rendered instruction — the document owns no lifecycle, so
// nothing else runs.
func TestTaskSetup_TaskDocumentInstance(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", map[string]string{"resource_kind": "pull", "revision": "sha2"}, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", nil)

	result, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID:      "review",
		SessionName: "org/repo-1",
		Resource:    "https://example.test/pull/1",
	})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	if result.Instance != "review#1" {
		t.Fatalf("Instance = %q, want review#1", result.Instance)
	}
	st := store.Get("org/repo-1").Tasks["review#1"]
	if st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("instance state = %+v", st)
	}
	if st.Observed == nil || st.Observed.State["revision"] != "sha2" {
		t.Errorf("observed = %+v, want the first observation of the resource", st.Observed)
	}
	if st.Observed != nil && st.Observed.At.IsZero() {
		t.Error("observation carries no timestamp, so a display cannot say how old it is")
	}
	if len(st.Outputs) != 0 {
		t.Errorf("outputs = %+v, want none: a task document produces nothing", st.Outputs)
	}
	if !strings.Contains(result.Instruction, "https://example.test/pull/1") {
		t.Errorf("instruction = %q, want the rendered body", result.Instruction)
	}
}

// A first observation that fails rejects instantiation: an instance that can
// never satisfy is worse than no instance.
func TestTaskSetup_TaskDocumentFirstObservationFails(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", nil, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", nil)

	_, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID:      "review",
		SessionName: "org/repo-1",
		Resource:    "https://example.test/pull/1",
	})
	if err == nil {
		t.Fatal("expected instantiation to be rejected when the first observation fails")
	}
	if got := store.Get("org/repo-1").Tasks["review#1"]; got != nil {
		t.Errorf("instance was created anyway: %+v", got)
	}
}

// The document states what kind of resource it is written for, so a resource
// no declared observer claims is refused at instantiation rather than
// producing an instance whose predicate can never read anything.
func TestTaskSetup_TaskDocumentResourceMustResolveToDeclaredObserver(t *testing.T) {
	store := testStore(t)
	cfg := writeTaskDocumentFixture(t, t.TempDir(), "wf", map[string]string{"resource_kind": "pull", "revision": "sha2"}, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", nil)

	_, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID:      "review",
		SessionName: "org/repo-1",
		Resource:    "does-not-match://x",
	})
	if err == nil {
		t.Fatal("expected instantiation to be rejected for a resource the declared observer does not claim")
	}
	if !strings.Contains(err.Error(), "issue_pr") {
		t.Errorf("error = %v, want it to name the declared observer", err)
	}
}

// The verdict flow, end to end: a review instance is pending until the
// reviewer records a verdict, satisfied once the recorded revision is the one
// the resource currently reports, and pending again after a new revision
// lands — with no derived output anywhere in it.
func TestTickSession_VerdictFlowThroughSelfState(t *testing.T) {
	store := testStore(t)
	revision := filepath.Join(t.TempDir(), "revision")
	if err := os.WriteFile(revision, []byte("sha2"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeObservedRevisionFixture(t, revision, reviewDocument)
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", nil)

	setup, err := TaskSetup(cfg, store, TaskSetupParams{
		TaskID: "review", SessionName: "org/repo-1", Resource: "https://example.test/pull/1",
	})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}

	action := tickActionFor(t, cfg, store, setup.Instance)
	if action.Action == "satisfied" {
		t.Fatalf("a review with no recorded verdict is not satisfied: %+v", action)
	}

	if _, err := SetTaskState(cfg, store, SetTaskStateParams{
		Identifier: "org/repo-1", Instance: setup.Instance, State: map[string]any{"verdict_revision": "sha2"},
	}); err != nil {
		t.Fatalf("SetTaskState: %v", err)
	}
	if action := tickActionFor(t, cfg, store, setup.Instance); action.Action != "satisfied" {
		t.Fatalf("a verdict recorded against the current revision satisfies: %+v", action)
	}

	// A new revision reopens the review on its own: the predicate compares
	// what was recorded with what the resource now reports.
	if err := os.WriteFile(revision, []byte("sha3"), 0o644); err != nil {
		t.Fatal(err)
	}
	if action := tickActionFor(t, cfg, store, setup.Instance); action.Action == "satisfied" {
		t.Fatalf("a verdict recorded against an older revision does not satisfy: %+v", action)
	}
}

// tickActionFor ticks the session and returns the action computed for one
// instance. A tick observes before it decides, so the assertion is about what
// the resource says now.
func tickActionFor(t *testing.T, cfg *config.Config, store *state.Store, instance string) CheckAction {
	t.Helper()
	result, err := TickSession(cfg, store, TickParams{SessionName: "org/repo-1", Trigger: TickTriggerManual})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	for _, a := range result.Actions {
		if a.Instance == instance {
			return a
		}
	}
	t.Fatalf("no action for %s: %+v", instance, result.Actions)
	return CheckAction{}
}

// writeObservedRevisionFixture writes an observer that reports the revision a
// file currently holds, so a test can move the resource forward.
func writeObservedRevisionFixture(t *testing.T, revisionFile string, documents ...string) *config.Config {
	t.Helper()
	baseDir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(baseDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("resources", "issue_pr.toml"), fmt.Sprintf(`
[issue_pr]
kind  = "resource_observer"
match = '^https://'

[issue_pr.observe]
type   = "shell"
script = '''
jq -nc --arg revision "$(cat %s)" '{resource_kind:"pull", revision:$revision}'
'''

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
resource_kind = { type = "string" }
revision      = { type = "string" }
`, revisionFile))
	write(filepath.Join("workflows", "wf.toml"), "[[nodes]]\nid = \"noop\"\n")
	for i, doc := range documents {
		write(filepath.Join("tasks", fmt.Sprintf("doc%d.md", i)), doc)
	}
	return &config.Config{WorkspaceDirsRoot: t.TempDir(), BaseDir: baseDir}
}
