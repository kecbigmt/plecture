package task

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// assertShellRequest checks the one request a shell action issued: the
// script its process runs, the cwd it runs in, and the environment its
// enclosing layers injected.
func assertShellRequest(t *testing.T, spy *effect.SpyExecutor, wantScript, wantDir string, wantEnv ...string) {
	t.Helper()
	if len(spy.Requests) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(spy.Requests), spy.Requests)
	}
	req := spy.Requests[0]
	if len(req.Argv) != 1 || !strings.HasSuffix(req.Argv[0], "run.sh") {
		t.Errorf("argv = %v, want the generated wrapper alone", req.Argv)
	}
	if spy.Scripts[0] != wantScript {
		t.Errorf("script = %q, want %q", spy.Scripts[0], wantScript)
	}
	if req.Dir != wantDir {
		t.Errorf("dir = %q, want %q", req.Dir, wantDir)
	}
	if req.Stdin != nil {
		t.Errorf("stdin = %q, want none", req.Stdin)
	}
	if !reflect.DeepEqual(req.Env, envOrNil(wantEnv)) {
		t.Errorf("env = %v, want %v", req.Env, wantEnv)
	}
}

func envOrNil(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	return env
}

// withSpyExecutor swaps the effect package's default executor for a spy for
// the duration of the test and restores it afterward, so tests never leak
// state into each other.
func withSpyExecutor(t *testing.T) *effect.SpyExecutor {
	t.Helper()
	spy := &effect.SpyExecutor{Stdout: []byte("{}")}
	restore := effect.UseExecutor(spy)
	t.Cleanup(restore)
	return spy
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
	assertShellRequest(t, spy, `echo '{"value":"x"}'`, "/work/x")
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
	assertShellRequest(t, spy, `echo cleaned`, "/work/x")
}

func TestExecutor_RunAliveProbeIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.Stdout = []byte("ok")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	if err := RunAliveProbe(context.Background(), Probe{Action: shellStub(`echo ok`)}, session); err != nil {
		t.Fatalf("alive probe: %v", err)
	}
	assertShellRequest(t, spy, `echo ok`, "/work/x")
}

func TestExecutor_RunCaptureIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.Stdout = []byte("pane contents")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	binding := &TerminalBinding{Ops: &config.TerminalConfig{Capture: shellStub(`tmux capture-pane`)}}
	out, err := RunCapture(context.Background(), binding, session)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if out != "pane contents" {
		t.Errorf("out = %q", out)
	}
	assertShellRequest(t, spy, `tmux capture-pane`, "/work/x")
}

func TestExecutor_ExecuteTaskSetupIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.Stdout = []byte(`{"ready":"yes"}`)
	def := config.TaskDefinition{ID: "review", Scope: "session", Setup: shellStub(`echo '{"ready":"yes"}'`)}
	r := resolveDef(t, def, "review#1")
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	result, err := ExecuteTaskSetup(context.Background(), r, nil, session, map[string]*contract.TaskState{})
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if result.Outputs["ready"] != "yes" {
		t.Errorf("outputs = %v", result.Outputs)
	}
	assertShellRequest(t, spy, `echo '{"ready":"yes"}'`, "/work/x")
}

func TestExecutor_RunActivityProbeIssuesExpectedExecRequest(t *testing.T) {
	spy := withSpyExecutor(t)
	spy.Stdout = []byte(`{"fingerprint":"f1"}`)
	session := SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}
	sig, err := RunActivityProbe(context.Background(), Probe{Action: shellStub(`probe`), Env: []string{"PLECT_GUARD=on"}}, session)
	if err != nil {
		t.Fatalf("activity probe: %v", err)
	}
	if sig == nil || sig.Fingerprint != "f1" {
		t.Fatalf("signal = %+v", sig)
	}
	assertShellRequest(t, spy, `probe`, "/work/x", "PLECT_GUARD=on")
}

// Standard input is an exec action's affordance; a shell action's values
// reach it through the binding transport instead. Pinning that here is what
// keeps a shared request-building path from quietly giving a shell action a
// stdin it never declared.
func TestExecutor_ShellActionsPassNoStdin(t *testing.T) {
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
			if err := RunAliveProbe(context.Background(), Probe{Action: shellStub(`echo ok`)}, session); err != nil {
				t.Fatal(err)
			}
		},
		"capture": func(t *testing.T) {
			binding := &TerminalBinding{Ops: &config.TerminalConfig{Capture: shellStub(`capture`)}}
			if _, err := RunCapture(context.Background(), binding, session); err != nil {
				t.Fatal(err)
			}
		},
		"dynamic instance setup": func(t *testing.T) {
			def := config.TaskDefinition{ID: "review", Scope: "session", Setup: shellStub(`echo '{}'`)}
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
			if len(spy.Requests) == 0 {
				t.Fatal("no request issued")
			}
			for i, req := range spy.Requests {
				if req.Stdin != nil {
					t.Errorf("request[%d].Stdin = %q, want none", i, req.Stdin)
				}
			}
		})
	}
}
