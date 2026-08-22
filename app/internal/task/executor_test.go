package task

import (
	"context"
	"reflect"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// spyExecutor records every ExecRequest it receives and returns a canned
// result, so tests can assert on argv/cwd without shelling out.
type spyExecutor struct {
	requests []ExecRequest
	stdout   []byte
	stderr   []byte
	err      error
}

func (s *spyExecutor) Run(ctx context.Context, req ExecRequest) (stdout, stderr []byte, err error) {
	s.requests = append(s.requests, req)
	return s.stdout, s.stderr, s.err
}

// withSpyExecutor swaps defaultExecutor for a spy for the duration of the
// test and restores it afterward, so tests never leak state into each other.
func withSpyExecutor(t *testing.T) *spyExecutor {
	t.Helper()
	spy := &spyExecutor{stdout: []byte("{}")}
	orig := defaultExecutor
	defaultExecutor = spy
	t.Cleanup(func() { defaultExecutor = orig })
	return spy
}

func wantExecRequest(cmdStr, workDir string) ExecRequest {
	return ExecRequest{Argv: []string{"bash", "-c", cmdStr}, Dir: workDir}
}

func TestExecutor_RunSetupIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{"value":"x"}'`}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`echo '{"value":"x"}'`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

func TestExecutor_RunCleanupIssuesExpectedExecRequest(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`, cleanup: `echo cleaned`}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	spy := withSpyExecutor(t)
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}, tasks, nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`echo cleaned`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

func TestExecutor_RunAliveProbeIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.stdout = []byte("ok")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	if err := RunAliveProbe(context.Background(), `echo ok`, map[string]any{}, map[string]any{}, session, ""); err != nil {
		t.Fatalf("alive probe: %v", err)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`echo ok`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

func TestExecutor_RunCaptureIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.stdout = []byte("pane contents")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	out, err := RunCapture(context.Background(), `tmux capture-pane`, map[string]any{}, session)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if out != "pane contents" {
		t.Errorf("out = %q", out)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`tmux capture-pane`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

func TestExecutor_FetchOutputIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.stdout = []byte("42")
	cfg := &config.Config{}
	src := config.DynamicOutput{Name: "count", Script: `echo 42`}
	ctx := RenderContext{Session: SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}}
	values, err := FetchOutput(context.Background(), cfg, src, ctx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if values["count"] != "42" {
		t.Errorf("count = %q", values["count"])
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`echo 42`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

func TestExecutor_ExecuteTaskSetupIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.stdout = []byte(`{"ready":"yes"}`)
	def := config.TaskDefinition{ID: "review", Scope: "session", Setup: `echo '{"ready":"yes"}'`}
	r := resolveDef(t, def, "review#1")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	result, err := ExecuteTaskSetup(context.Background(), r, nil, session, map[string]*contract.TaskState{})
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if result.Outputs["ready"] != "yes" {
		t.Errorf("outputs = %v", result.Outputs)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`echo '{"ready":"yes"}'`, "/work/x")
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

// TestExecutor_RunShellIsNeverRoutedThroughDefaultExecutor guards the
// distinction between alwaysHostExecutor (runShell — provider setup/cleanup,
// provider subscribe, resource observe/finalize) and defaultExecutor
// (execHostScript — the 5 task exec paths). Swapping defaultExecutor for a
// spy must never intercept runShell: those callers must always run for real
// on the host.
func TestExecutor_RunShellIsNeverRoutedThroughDefaultExecutor(t *testing.T) {
	spy := withSpyExecutor(t)
	stdout, _, err := runShell(`echo real`, "")
	if err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if string(stdout) != "real\n" {
		t.Errorf("stdout = %q, want runShell to actually execute (not the spy's canned output)", stdout)
	}
	if len(spy.requests) != 0 {
		t.Errorf("spy recorded %d requests, want 0: runShell must not consult defaultExecutor", len(spy.requests))
	}
}

// TestExecutor_HostExecutorReproducesRunShellSemantics locks in byte-for-byte
// parity with the pre-Executor runShell: hostExecutor.Run must behave
// exactly like it (bash -c, cwd only if it exists).
func TestExecutor_HostExecutorReproducesRunShellSemantics(t *testing.T) {
	var exec Executor = hostExecutor{}
	stdout, stderr, err := exec.Run(context.Background(), ExecRequest{Argv: []string{"bash", "-c", `echo out; echo err >&2`}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(stdout) != "out\n" || string(stderr) != "err\n" {
		t.Errorf("stdout=%q stderr=%q", stdout, stderr)
	}

	// A Dir that doesn't exist must be silently ignored, not surfaced as an
	// error — the exact behavior runShell has always had.
	stdout, _, err = exec.Run(context.Background(), ExecRequest{Argv: []string{"bash", "-c", "pwd"}, Dir: "/nonexistent/does-not-exist"})
	if err != nil {
		t.Fatalf("Run with missing Dir: %v", err)
	}
	if string(stdout) == "/nonexistent/does-not-exist\n" {
		t.Errorf("Dir was applied despite not existing")
	}
}

func TestExecutor_RunActivityProbeIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.stdout = []byte(`{"fingerprint":"f1"}`)
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	sig, err := RunActivityProbe(context.Background(), `probe`, map[string]any{}, map[string]any{}, session, "", "PLECT_GUARD=on")
	if err != nil {
		t.Fatalf("activity probe: %v", err)
	}
	if sig == nil || sig.Fingerprint != "f1" {
		t.Fatalf("signal = %+v", sig)
	}
	if len(spy.requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.requests), spy.requests)
	}
	want := wantExecRequest(`probe`, "/work/x")
	want.Env = []string{"PLECT_GUARD=on"}
	if !reflect.DeepEqual(spy.requests[0], want) {
		t.Errorf("request = %+v, want %+v", spy.requests[0], want)
	}
}

// Standard input is an exec action's affordance, and no template-rendered
// hook has one. Pinning that here is what keeps a shared request-building
// path from quietly giving a legacy hook a stdin it never declared.
func TestExecutor_TemplateRenderedHooksPassNoStdin(t *testing.T) {
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	paths := map[string]func(t *testing.T){
		"task setup": func(t *testing.T) {
			plan := buildPlan(t,
				[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`}},
				[]nodeStub{{id: "a"}},
			)
			if err := RunSetup(context.Background(), plan.Run, session, map[string]*contract.TaskState{}, nil); err != nil {
				t.Fatal(err)
			}
		},
		"alive probe": func(t *testing.T) {
			if err := RunAliveProbe(context.Background(), `echo ok`, map[string]any{}, map[string]any{}, session, ""); err != nil {
				t.Fatal(err)
			}
		},
		"capture": func(t *testing.T) {
			if _, err := RunCapture(context.Background(), `capture`, map[string]any{}, session); err != nil {
				t.Fatal(err)
			}
		},
		"dynamic output": func(t *testing.T) {
			src := config.DynamicOutput{Name: "count", Script: `echo 1`}
			if _, err := FetchOutput(context.Background(), &config.Config{}, src, RenderContext{Session: session}); err != nil {
				t.Fatal(err)
			}
		},
		"dynamic instance setup": func(t *testing.T) {
			def := config.TaskDefinition{ID: "review", Scope: "session", Setup: `echo '{}'`}
			r := resolveDef(t, def, "review#1")
			if _, err := ExecuteTaskSetup(context.Background(), r, nil, session, map[string]*contract.TaskState{}); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			spy := withSpyExecutor(t)
			run(t)
			if len(spy.requests) == 0 {
				t.Fatal("no request issued")
			}
			for i, req := range spy.requests {
				if req.Stdin != nil {
					t.Errorf("request[%d].Stdin = %q, want none", i, req.Stdin)
				}
			}
		})
	}
}

func TestExecutor_RequestForKeepsEachFormsInvocationShape(t *testing.T) {
	tests := []struct {
		name      string
		execution *lang.Execution
		workDir   string
		env       []string
		want      ExecRequest
	}{
		{
			name:      "a template-rendered hook",
			execution: renderedShell(`echo '{}'`),
			workDir:   "/work/x",
			want:      ExecRequest{Argv: []string{"bash", "-c", `echo '{}'`}, Dir: "/work/x"},
		},
		{
			name:      "a rendered hook carrying an enclosing layer's env",
			execution: renderedShell(`echo hi`),
			workDir:   "/work/x",
			env:       []string{"PLECT_GUARD=on"},
			want:      ExecRequest{Argv: []string{"bash", "-c", `echo hi`}, Dir: "/work/x", Env: []string{"PLECT_GUARD=on"}},
		},
		{
			name:      "a resolved action",
			execution: &lang.Execution{Argv: []string{"/plugins/bin/okf-goal", "resource", "finalize"}, Stdin: []byte(`[]`)},
			want:      ExecRequest{Argv: []string{"/plugins/bin/okf-goal", "resource", "finalize"}, Stdin: []byte(`[]`)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requestFor(tt.execution, tt.workDir, tt.env)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("requestFor = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExecutor_RunHookIsNeverRoutedThroughDefaultExecutor(t *testing.T) {
	spy := withSpyExecutor(t)
	stdout, _, err := runHook(context.Background(), &lang.Execution{Argv: []string{"echo", "real"}}, "")
	if err != nil {
		t.Fatalf("runHook: %v", err)
	}
	if string(stdout) != "real\n" {
		t.Errorf("stdout = %q, want runHook to actually execute", stdout)
	}
	if len(spy.requests) != 0 {
		t.Errorf("spy recorded %d requests, want 0", len(spy.requests))
	}
}
