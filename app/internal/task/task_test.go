package task

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/plecture/plect/contracts/state"
)

// recordingObserver captures observer events for assertions.
type recordingObserver struct {
	successes []string
	failures  []string
	skips     []string
}

func (r *recordingObserver) OnStart(string, string) {}
func (r *recordingObserver) OnSuccess(_, id string, _ time.Duration, _ []byte) {
	r.successes = append(r.successes, id)
}
func (r *recordingObserver) OnFailure(_, id string, _ time.Duration, _ error, _ []byte) {
	r.failures = append(r.failures, id)
}
func (r *recordingObserver) OnSkip(_, id, _ string) { r.skips = append(r.skips, id) }

// depInput wires a node to its upstream by referencing the upstream's outputs
// in an input binding. CompileWorkflow derives the DAG from these references,
// so test scenarios that used to thread `DependsOn` now thread a synthetic
// input — the regex-based deriveDependsOn doesn't care that the upstream key
// is fictitious.
func depInput(upstream string) map[string]string {
	return map[string]string{
		"link": "{{.Nodes." + upstream + ".outputs.k}}",
	}
}

func TestPlan_DuplicateNodeID(t *testing.T) {
	_, err := tryBuildPlan(
		[]taskStub{{id: "a", scope: "run", setup: "true"}},
		[]nodeStub{{id: "a"}, {id: "a"}},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestPlan_RunMayDependOnSession(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{
			{id: "envfile", scope: "session", setup: "true"},
			{id: "tmux", scope: "run", setup: "true"},
		},
		[]nodeStub{
			{id: "envfile"},
			{id: "tmux", inputs: depInput("envfile")},
		},
	)
	if len(plan.Session) != 1 || plan.Session[0].NodeID != "envfile" {
		t.Fatalf("expected envfile in session plan, got %+v", plan.Session)
	}
	if len(plan.Run) != 1 || plan.Run[0].NodeID != "tmux" {
		t.Fatalf("expected tmux in run plan, got %+v", plan.Run)
	}
}

func TestPlan_TopoSort(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{
			{id: "claude", scope: "run", setup: "true"},
			{id: "tmux", scope: "run", setup: "true"},
		},
		[]nodeStub{
			{id: "claude", inputs: depInput("tmux")},
			{id: "tmux"},
		},
	)
	if got := ids(plan.Run); got[0] != "tmux" || got[1] != "claude" {
		t.Fatalf("expected [tmux claude], got %v", got)
	}
}

func TestPlan_TopoSort_Diamond(t *testing.T) {
	// d depends on b,c; b,c depend on a. Order must be: a first, d last,
	// b,c somewhere between. Catches off-by-one bugs in indegree updates.
	plan := buildPlan(t,
		[]taskStub{
			{id: "a", scope: "run", setup: "true"},
			{id: "b", scope: "run", setup: "true"},
			{id: "c", scope: "run", setup: "true"},
			{id: "d", scope: "run", setup: "true"},
		},
		[]nodeStub{
			{id: "a"},
			{id: "d", inputs: map[string]string{
				"b": "{{.Nodes.b.outputs.k}}",
				"c": "{{.Nodes.c.outputs.k}}",
			}},
			{id: "b", inputs: depInput("a")},
			{id: "c", inputs: depInput("a")},
		},
	)
	order := ids(plan.Run)
	if len(order) != 4 {
		t.Fatalf("expected 4 nodes, got %v", order)
	}
	if order[0] != "a" {
		t.Fatalf("expected a first, got %v", order)
	}
	if order[3] != "d" {
		t.Fatalf("expected d last, got %v", order)
	}
	mid := map[string]bool{order[1]: true, order[2]: true}
	if !mid["b"] || !mid["c"] {
		t.Fatalf("expected {b,c} between a and d, got %v", order)
	}
}

func TestPlan_TopoSort_SiblingDeclarationOrder(t *testing.T) {
	// Independent tasks keep declaration order — users rely on this for
	// "envfile before dotenv" type ergonomics without writing depends_on.
	plan := buildPlan(t,
		[]taskStub{
			{id: "envfile", scope: "run", setup: "true"},
			{id: "dotenv", scope: "run", setup: "true"},
		},
		[]nodeStub{{id: "envfile"}, {id: "dotenv"}},
	)
	if got := ids(plan.Run); got[0] != "envfile" || got[1] != "dotenv" {
		t.Fatalf("expected [envfile dotenv], got %v", got)
	}
	plan2 := buildPlan(t,
		[]taskStub{
			{id: "envfile", scope: "run", setup: "true"},
			{id: "dotenv", scope: "run", setup: "true"},
		},
		[]nodeStub{{id: "dotenv"}, {id: "envfile"}},
	)
	if got := ids(plan2.Run); got[0] != "dotenv" || got[1] != "envfile" {
		t.Fatalf("expected [dotenv envfile], got %v", got)
	}
}

func TestPlan_TopoSort_CrossScope(t *testing.T) {
	// claude depends on tmux (run) AND envfile (session). The run plan must
	// only topo-sort within run scope — envfile must not contribute to claude's
	// indegree, otherwise claude would never become ready in the run plan.
	plan := buildPlan(t,
		[]taskStub{
			{id: "envfile", scope: "session", setup: "true"},
			{id: "claude", scope: "run", setup: "true"},
			{id: "tmux", scope: "run", setup: "true"},
		},
		[]nodeStub{
			{id: "envfile"},
			{id: "claude", inputs: map[string]string{
				"tmux":    "{{.Nodes.tmux.outputs.k}}",
				"envfile": "{{.Nodes.envfile.outputs.k}}",
			}},
			{id: "tmux"},
		},
	)
	if got := ids(plan.Session); len(got) != 1 || got[0] != "envfile" {
		t.Fatalf("session plan: expected [envfile], got %v", got)
	}
	if got := ids(plan.Run); len(got) != 2 || got[0] != "tmux" || got[1] != "claude" {
		t.Fatalf("run plan: expected [tmux claude], got %v", got)
	}
}

func ids(rs []Resolved) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.NodeID
	}
	return out
}

func TestPlan_InvalidScope(t *testing.T) {
	_, err := tryBuildPlan(
		[]taskStub{{id: "a", scope: "bogus", setup: "true"}},
		[]nodeStub{{id: "a"}},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("expected invalid scope error, got %v", err)
	}
}

func TestParseOutputs(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  map[string]any
		isErr bool
	}{
		{"empty", "", map[string]any{}, false},
		{"whitespace", "  \n  ", map[string]any{}, false},
		{"object", `{"pid":123}`, map[string]any{"pid": float64(123)}, false},
		{"invalid", `not json`, nil, true},
		{"array", `[1,2]`, nil, true},
		{"null", `null`, nil, true},
		{"null_with_whitespace", "  null  ", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseOutputs([]byte(c.in))
			if c.isErr {
				if err == nil {
					t.Fatalf("expected error, got nil (output=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("output length mismatch: got %v want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("key %q: got %v want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestRender_SelfAndTasks(t *testing.T) {
	out, err := render(`{{.Self.path}}-{{.Tasks.tmux.session_name}}-{{.SessionName}}`, RenderContext{
		Self:    map[string]any{"path": "/tmp/.env"},
		Tasks:   map[string]map[string]any{"tmux": {"session_name": "foo"}},
		Session: SessionVars{Name: "sess"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "/tmp/.env-foo-sess" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRender_MissingKeyIsError(t *testing.T) {
	_, err := render(`{{.Tasks.unknown.x}}`, RenderContext{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// `get` provides a missingkey-safe lookup for callers that need to read
// previous-run outputs (via .Prev) under the strict setup template option.
func TestRender_GetReturnsPresentValue(t *testing.T) {
	out, err := render(`{{get .Prev "session_id"}}`, RenderContext{
		Prev: map[string]any{"session_id": "abc-123"},
	})
	if err != nil || out != "abc-123" {
		t.Fatalf("got (%q, %v), want (%q, nil)", out, err, "abc-123")
	}
}

func TestRender_GetReturnsEmptyWhenAbsent(t *testing.T) {
	out, err := render(`X={{get .Prev "session_id"}}Y`, RenderContext{
		Prev: map[string]any{}, // no session_id key
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "X=Y" {
		t.Fatalf("got %q, want %q", out, "X=Y")
	}
}

func TestRender_GetReturnsEmptyWhenPrevNil(t *testing.T) {
	out, err := render(`{{get .Prev "anything"}}`, RenderContext{Prev: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q, want empty", out)
	}
}

// Cleanup must survive a setup that failed before populating Self outputs.
// Task scripts are expected to be shell-defensive against empty values.
func TestRenderCleanup_MissingKeyRendersAsZero(t *testing.T) {
	out, err := renderCleanup(
		`kill -TERM {{.Self.pid}} || exit 0; rm -f {{.Self.mcp_config}}`,
		RenderContext{Self: map[string]any{}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "kill -TERM  || exit 0; rm -f "
	if out != want {
		t.Fatalf("unexpected output: got %q want %q", out, want)
	}
}

func TestRenderCleanup_PresentKeyStillRenders(t *testing.T) {
	out, err := renderCleanup(
		`kill -TERM {{.Self.pid}}`,
		RenderContext{Self: map[string]any{"pid": 12345}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "kill -TERM 12345" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRunSetup_CapturesOutputsAndRespectsDeps(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{
			{id: "a", scope: "run", setup: `echo '{"value":"first"}'`},
			{id: "b", scope: "run", setup: `echo "{\"saw\":\"{{.Tasks.a.value}}\"}"`},
		},
		[]nodeStub{
			{id: "a"},
			{id: "b", inputs: map[string]string{"a_dep": "{{.Nodes.a.outputs.value}}"}},
		},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{Name: "x"}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["a"].Outputs["value"] != "first" {
		t.Fatalf("a.value = %v", tasks["a"].Outputs["value"])
	}
	if tasks["b"].Outputs["saw"] != "first" {
		t.Fatalf("b.saw = %v", tasks["b"].Outputs["saw"])
	}
	if tasks["a"].Status != contract.TaskStatusProduced {
		t.Fatalf("a.Status = %q", tasks["a"].Status)
	}
}

func TestRunSetup_SkipsProduced(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	tmpDir := t.TempDir()
	marker := tmpDir + "/setup-ran"
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "touch " + marker + "; echo '{}'"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{"value": "preserved"}},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, statErr := exec.Command("bash", "-c", "test -f "+marker).CombinedOutput(); statErr == nil {
		t.Fatal("setup script ran for an already-produced task")
	}
	if tasks["a"].Outputs["value"] != "preserved" {
		t.Fatalf("expected preserved outputs, got %v", tasks["a"].Outputs)
	}
}

func TestRunSetup_RetriesFailed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{"value":"second"}'`}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusFailed, Error: "first attempt"},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["a"].Status != contract.TaskStatusProduced {
		t.Fatalf("status = %q, want produced", tasks["a"].Status)
	}
	if tasks["a"].Outputs["value"] != "second" {
		t.Fatalf("outputs not refreshed: %v", tasks["a"].Outputs)
	}
}

func TestRunSetup_RetriesCleaned(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{"ok":true}'`}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusCleaned},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["a"].Status != contract.TaskStatusProduced {
		t.Fatalf("status = %q, want produced", tasks["a"].Status)
	}
}

// Setup must see the previous run's outputs via .Prev when a cleaned state
// is replayed (e.g. `plect down` followed by `plect up`). This is what enables
// tasks like claude to keep a stable session_id across restarts.
func TestRunSetup_PrevCarriesPreviousOutputs(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{
			id:    "claude",
			scope: "run",
			setup: `echo "{\"session_id\":\"{{get .Prev "session_id"}}\"}"`,
		}},
		[]nodeStub{{id: "claude"}},
	)
	tasks := map[string]*contract.TaskState{
		"claude": {
			Scope:   "run",
			Status:  contract.TaskStatusCleaned,
			Outputs: map[string]any{"session_id": "kept-across-down"},
		},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, _ := tasks["claude"].Outputs["session_id"].(string)
	if got != "kept-across-down" {
		t.Fatalf("session_id = %q, want %q (was the previous run's value)", got, "kept-across-down")
	}
}

// First-ever setup has no Prev. `get` must return empty, not error.
func TestRunSetup_PrevEmptyOnFirstRun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{
			id:    "claude",
			scope: "run",
			setup: `echo '{"session_id":"{{get .Prev "session_id"}}"}'`,
		}},
		[]nodeStub{{id: "claude"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, _ := tasks["claude"].Outputs["session_id"].(string)
	if got != "" {
		t.Fatalf("session_id = %q, want empty on first run", got)
	}
}

// Regression: when setup fails, the previously produced
// outputs must survive into the next attempt as .Prev — otherwise tasks
// that key off a stable identifier (claude session_id etc.) cannot recover
// after a transient failure.
func TestRunSetup_FailurePreservesPrevOutputs(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "exit 1"}},
		[]nodeStub{{id: "a"}},
	)
	// Prior run completed and was torn down (Cleaned) — its outputs must
	// survive the next failed setup attempt so the run after that can still
	// see them via .Prev.
	tasks := map[string]*contract.TaskState{
		"a": {
			Scope:   "run",
			Status:  contract.TaskStatusCleaned,
			Outputs: map[string]any{"session_id": "kept-across-retry", "pid": float64(12345)},
		},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected setup error")
	}
	if tasks["a"].Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", tasks["a"].Status)
	}
	if got, _ := tasks["a"].Outputs["session_id"].(string); got != "kept-across-retry" {
		t.Fatalf("session_id = %v, want preserved across failure", tasks["a"].Outputs["session_id"])
	}
	// Numeric outputs must survive too — `pid` etc. drive resume logic.
	if got, _ := tasks["a"].Outputs["pid"].(float64); got != 12345 {
		t.Fatalf("pid = %v (%T), want 12345 preserved across failure",
			tasks["a"].Outputs["pid"], tasks["a"].Outputs["pid"])
	}
}

// Regression: JSON numbers unmarshal into float64, and Go's
// default formatter renders large floats as scientific notation (e.g.
// 3.052179e+06). Scripts compare the rendered value as a string, so the
// renderer must emit integer-valued numbers without exponent.
func TestRender_IntegerFloatNotScientific(t *testing.T) {
	out, err := render(`pid={{get .Prev "pid"}};self={{.Self.pid}}`, RenderContext{
		Self: map[string]any{"pid": float64(3052179)},
		Prev: map[string]any{"pid": float64(3052179)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "pid=3052179;self=3052179"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// Normalization must reach nested maps/slices reachable through .Tasks too,
// otherwise an upstream task's deep `pid` could still leak as scientific.
func TestRender_IntegerFloatNormalizedInNestedTasks(t *testing.T) {
	out, err := render(
		`{{.Tasks.dep.meta.pid}}-{{index .Tasks.dep.pids 0}}`,
		RenderContext{
			Tasks: map[string]map[string]any{
				"dep": {
					"meta": map[string]any{"pid": float64(3052179)},
					"pids": []any{float64(3052179)},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "3052179-3052179" {
		t.Fatalf("got %q, want %q", out, "3052179-3052179")
	}
}

// Non-integer floats keep their decimal form — only integer-valued floats
// are normalized.
func TestRender_NonIntegerFloatPreserved(t *testing.T) {
	out, err := render(`x={{.Self.ratio}}`, RenderContext{
		Self: map[string]any{"ratio": 0.5},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "x=0.5" {
		t.Fatalf("got %q, want %q", out, "x=0.5")
	}
}

func TestRunSetup_FailureMarksFailed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "exit 1"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected error")
	}
	if tasks["a"].Status != contract.TaskStatusFailed {
		t.Fatalf("a.Status = %q", tasks["a"].Status)
	}
}

func TestRunCleanup_ReverseOrder(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{
			{id: "a", scope: "run", setup: "echo '{}'", cleanup: "true"},
			{id: "b", scope: "run", setup: "echo '{}'", cleanup: "true"},
		},
		[]nodeStub{
			{id: "a"},
			{id: "b", inputs: depInput("a")},
		},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		"b": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if tasks["a"].Status != contract.TaskStatusCleaned {
		t.Fatalf("a.Status = %q", tasks["a"].Status)
	}
	if tasks["b"].Status != contract.TaskStatusCleaned {
		t.Fatalf("b.Status = %q", tasks["b"].Status)
	}
}

// Regression: a setup that exited before populating Self leaves Outputs nil.
// Cleanup that references Self.* must still run (with empty substitutions)
// rather than aborting the entire teardown with a template render error.
func TestRunCleanup_FailedSetupRendersZeroes(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{
			id:      "a",
			scope:   "run",
			setup:   "exit 1",
			cleanup: `kill -TERM "{{.Self.pid}}" 2>/dev/null || true; rm -f "{{.Self.path}}"`,
		}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusFailed, Outputs: nil},
	}
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("cleanup should not fail when Self keys are missing: %v", err)
	}
	if tasks["a"].Status != contract.TaskStatusCleaned {
		t.Fatalf("a.Status = %q, want cleaned", tasks["a"].Status)
	}
}

// A cleanup script that errors must flip the task's Status to failed.
// The status table treats "failed" as covering both setup and cleanup
// errors — leaving the entity at "produced" after a failed teardown would
// hide the broken state from later `plect up` (which skips produced tasks).
func TestRunCleanup_FailureFlipsToFailed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "true", cleanup: "exit 1"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected cleanup error")
	}
	if tasks["a"].Status != contract.TaskStatusFailed {
		t.Fatalf("a.Status = %q, want failed", tasks["a"].Status)
	}
	if tasks["a"].Error == "" {
		t.Fatal("expected Error to be populated on cleanup failure")
	}
}

// writeSchema writes a JSON schema to a temp file and returns its absolute path.
func writeSchema(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "outputs.schema.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return path
}

// Misconfigured schemas must fail at plan-build, not silently mid-setup.
func TestPlan_CompilesOutputsSchema(t *testing.T) {
	schemaPath := writeSchema(t, `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "envfile", scope: "session", setup: "true", outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "envfile"}},
	)
	if len(plan.Session) != 1 || plan.Session[0].OutputsSchema == nil {
		t.Fatalf("expected compiled schema on Resolved, got %+v", plan.Session)
	}
}

func TestPlan_MissingSchemaFileErrors(t *testing.T) {
	_, err := tryBuildPlan(
		[]taskStub{{id: "envfile", scope: "session", setup: "true", outputsSchemaFile: "/does/not/exist.json"}},
		[]nodeStub{{id: "envfile"}},
	)
	if err == nil || !strings.Contains(err.Error(), "outputs schema") {
		t.Fatalf("expected outputs schema error, got %v", err)
	}
}

func TestPlan_InvalidSchemaErrors(t *testing.T) {
	schemaPath := writeSchema(t, `{not json`)
	_, err := tryBuildPlan(
		[]taskStub{{id: "envfile", scope: "session", setup: "true", outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "envfile"}},
	)
	if err == nil || !strings.Contains(err.Error(), "outputs schema") {
		t.Fatalf("expected outputs schema error, got %v", err)
	}
}

func TestRunSetup_ValidatesOutputsAgainstSchema_Valid(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	schemaPath := writeSchema(t, `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "envfile", scope: "session", setup: `echo '{"path":"/tmp/.env"}'`, outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "envfile"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Session, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["envfile"].Status != contract.TaskStatusProduced {
		t.Fatalf("status = %q, want produced", tasks["envfile"].Status)
	}
	if tasks["envfile"].Outputs["path"] != "/tmp/.env" {
		t.Fatalf("outputs = %v", tasks["envfile"].Outputs)
	}
}

func TestRunSetup_SchemaRejectsMissingRequired(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	schemaPath := writeSchema(t, `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "envfile", scope: "session", setup: `echo '{"pid":42}'`, outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "envfile"}},
	)
	tasks := map[string]*contract.TaskState{}
	runErr := RunSetup(context.Background(), plan.Session, SessionVars{}, tasks, nil)
	if runErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(runErr.Error(), "outputs schema") {
		t.Fatalf("error should mention outputs schema, got: %v", runErr)
	}
	st := tasks["envfile"]
	if st.Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", st.Status)
	}
	if st.Error == "" {
		t.Fatal("Error should be populated with validation detail")
	}
	if st.Outputs != nil {
		t.Fatalf("Outputs should be unset on schema failure, got %v", st.Outputs)
	}
}

func TestRunSetup_EmptySetupValidatesOutputsAgainstSchema(t *testing.T) {
	schemaPath := writeSchema(t, `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "envfile", scope: "session", setup: "", outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "envfile"}},
	)
	tasks := map[string]*contract.TaskState{}
	runErr := RunSetup(context.Background(), plan.Session, SessionVars{}, tasks, nil)
	if runErr == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(runErr.Error(), "outputs schema") {
		t.Fatalf("error should mention outputs schema, got: %v", runErr)
	}
	st := tasks["envfile"]
	if st.Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", st.Status)
	}
	if st.Outputs != nil {
		t.Fatalf("Outputs should be unset on schema failure, got %v", st.Outputs)
	}
}

func TestRunSetup_SchemaRejectsWrongType(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	schemaPath := writeSchema(t, `{"type":"object","properties":{"pid":{"type":"integer"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "p", scope: "run", setup: `echo '{"pid":"not-a-number"}'`, outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "p"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected schema validation error")
	}
	if tasks["p"].Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", tasks["p"].Status)
	}
}

func TestRunSetup_SchemaRejectsAdditionalProperties(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	schemaPath := writeSchema(t, `{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"}}}`)
	plan := buildPlan(t,
		[]taskStub{{id: "e", scope: "run", setup: `echo '{"path":"/x","extra":1}'`, outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "e"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected schema validation error")
	}
	if tasks["e"].Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", tasks["e"].Status)
	}
}

// Backward compatibility: no schema = any JSON object accepted.
func TestRunSetup_NoSchemaAcceptsAnyObject(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "free", scope: "run", setup: `echo '{"anything":[1,2,3],"goes":true}'`}},
		[]nodeStub{{id: "free"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["free"].Status != contract.TaskStatusProduced {
		t.Fatalf("status = %q, want produced", tasks["free"].Status)
	}
}

func TestPlan_InlineOutputsSchema(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: "true", outputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"session_name"},
			"properties": map[string]any{
				"session_name": map[string]any{"type": "string"},
			},
		}}},
		[]nodeStub{{id: "tmux"}},
	)
	if plan.Run[0].OutputsSchema == nil {
		t.Fatal("expected inline schema to compile")
	}
}

func TestPlan_InlineAndFileMutuallyExclusive(t *testing.T) {
	schemaPath := writeSchema(t, `{"type":"object"}`)
	_, err := tryBuildPlan(
		[]taskStub{{id: "x", scope: "run", setup: "true",
			outputsSchema:     map[string]any{"type": "object"},
			outputsSchemaFile: schemaPath}},
		[]nodeStub{{id: "x"}},
	)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually-exclusive error, got %v", err)
	}
}

func TestRunSetup_InlineSchemaRejectsMissingRequired(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: `echo '{}'`, outputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"session_name"},
			"properties": map[string]any{
				"session_name": map[string]any{"type": "string"},
			},
		}}},
		[]nodeStub{{id: "tmux"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("expected schema validation error")
	}
	if tasks["tmux"].Status != contract.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", tasks["tmux"].Status)
	}
}

func TestPlan_ResolvesSchemaPathAgainstBaseDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.schema.json"),
		[]byte(`{"type":"object","required":["path"]}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	plan := buildPlan(t,
		[]taskStub{{id: "envfile", scope: "session", setup: "true",
			outputsSchemaFile: "out.schema.json", baseDir: dir}},
		[]nodeStub{{id: "envfile"}},
	)
	if plan.Session[0].OutputsSchema == nil {
		t.Fatal("expected schema to compile via BaseDir resolution")
	}
}

// A task with no cleanup command still reaches the terminal "cleaned"
// state — the entity is released by definition. The observer must see it
// as a success, not a skip, so progress UIs render ✓ rather than ⊘.
func TestRunCleanup_EmptyCleanupFiresOnSuccess(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "true"}}, // no cleanup
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	obs := &recordingObserver{}
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{}, tasks, obs); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if tasks["a"].Status != contract.TaskStatusCleaned {
		t.Fatalf("a.Status = %q, want cleaned", tasks["a"].Status)
	}
	if len(obs.successes) != 1 || obs.successes[0] != "a" {
		t.Fatalf("expected OnSuccess for 'a', got successes=%v skips=%v", obs.successes, obs.skips)
	}
	if len(obs.skips) != 0 {
		t.Fatalf("empty cleanup should not be reported as skipped, got skips=%v", obs.skips)
	}
}

func TestPlan_MultipleAttachRejected(t *testing.T) {
	_, err := tryBuildPlan(
		[]taskStub{
			{id: "tmux", scope: "run", setup: "true", attach: "tmux attach"},
			{id: "zellij", scope: "run", setup: "true", attach: "zellij attach"},
		},
		[]nodeStub{{id: "tmux"}, {id: "zellij"}},
	)
	if err == nil || !strings.Contains(err.Error(), "more than one node declares attach") {
		t.Fatalf("expected multi-attach error, got %v", err)
	}
}

func TestPlan_SingleAttachAccepted(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: "true", attach: "tmux attach -t {{.Self.session_name}}"}},
		[]nodeStub{{id: "tmux"}},
	)
	target := plan.AttachTask()
	if target == nil {
		t.Fatal("expected attach task on plan")
	}
	if target.NodeID != "tmux" {
		t.Fatalf("attach task = %q, want tmux", target.NodeID)
	}
}

func TestPlan_AttachTaskNilWhenNoneDeclared(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: "true"}},
		[]nodeStub{{id: "tmux"}},
	)
	if plan.AttachTask() != nil {
		t.Fatal("expected nil attach task when none declared")
	}
}

func TestRenderAttach_ExpandsSelfAndSessionVars(t *testing.T) {
	out, err := RenderAttach(
		"tmux attach -t {{.Self.session_name}} ({{.SessionName}})",
		map[string]any{"session_name": "owner/repo-1"},
		SessionVars{Name: "owner/repo-1"},
	)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "tmux attach -t owner/repo-1 (owner/repo-1)"
	if out != want {
		t.Fatalf("render = %q, want %q", out, want)
	}
}

func TestRenderAttach_MissingSelfKeyErrors(t *testing.T) {
	// Attach uses strict missingkey semantics — a missing .Self.<key> is a
	// contract violation, not a silently-empty TTY command.
	_, err := RenderAttach(
		"tmux attach -t {{.Self.session_name}}",
		map[string]any{},
		SessionVars{},
	)
	if err == nil {
		t.Fatal("expected error for missing Self key")
	}
}

func TestPlan_CaptureTaskNilWhenNoneDeclared(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: "true"}},
		[]nodeStub{{id: "tmux"}},
	)
	target, err := plan.CaptureTask()
	if err != nil {
		t.Fatalf("CaptureTask: %v", err)
	}
	if target != nil {
		t.Fatal("expected nil capture task when none declared")
	}
}

func TestPlan_CaptureTaskReturnsDeclaringNode(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "tmux", scope: "run", setup: "true", capture: "tmux capture-pane -p -t {{.Self.session_name}}"}},
		[]nodeStub{{id: "tmux"}},
	)
	target, err := plan.CaptureTask()
	if err != nil {
		t.Fatalf("CaptureTask: %v", err)
	}
	if target == nil || target.NodeID != "tmux" {
		t.Fatalf("CaptureTask = %+v, want node %q", target, "tmux")
	}
}

func TestPlan_CaptureTaskAmbiguousWhenMultipleDeclare(t *testing.T) {
	// Unlike attach (a compile-time "at most one" validation), capture allows
	// any number of task definitions to declare it; ambiguity across the
	// resolved plan is a runtime resolution error instead.
	plan := buildPlan(t,
		[]taskStub{
			{id: "tmux", scope: "run", setup: "true", capture: "tmux capture-pane -p -t {{.Self.session_name}}"},
			{id: "other", scope: "run", setup: "true", capture: "echo other"},
		},
		[]nodeStub{{id: "tmux"}, {id: "other"}},
	)
	target, err := plan.CaptureTask()
	if err == nil {
		t.Fatalf("expected ambiguous-capture error, got target %+v", target)
	}
	if !strings.Contains(err.Error(), "tmux") || !strings.Contains(err.Error(), "other") {
		t.Fatalf("error %q should name both ambiguous node ids", err.Error())
	}
}

func TestRenderCapture_ExpandsSelfAndSessionVars(t *testing.T) {
	out, err := RunCapture(context.Background(),
		"echo -n 'view of {{.Self.session_name}} ({{.SessionName}})'",
		map[string]any{"session_name": "owner/repo-1"},
		SessionVars{Name: "owner/repo-1"},
	)
	if err != nil {
		t.Fatalf("RunCapture: %v", err)
	}
	want := "view of owner/repo-1 (owner/repo-1)"
	if out != want {
		t.Fatalf("RunCapture = %q, want %q", out, want)
	}
}

func TestRunCapture_MissingSelfKeyErrors(t *testing.T) {
	_, err := RunCapture(context.Background(),
		"echo {{.Self.session_name}}",
		map[string]any{},
		SessionVars{},
	)
	if err == nil {
		t.Fatal("expected error for missing Self key")
	}
}

func TestRunCapture_SurfacesStderrOnFailure(t *testing.T) {
	// An orphaned pane (script exits non-zero) must be a hard error, not a
	// success with empty output.
	_, err := RunCapture(context.Background(),
		`echo "can't find pane" >&2; exit 1`,
		map[string]any{},
		SessionVars{},
	)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "can't find pane") {
		t.Fatalf("error %q should carry stderr", err.Error())
	}
}
