package task

import (
	"context"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestResolveExecution(t *testing.T) {
	cases := []struct {
		name        string
		declared    string
		environment string
		want        string
		wantErr     bool
	}{
		{"unset, no environment defaults host", "", "", config.ExecutionHost, false},
		{"unset, explicit host workflow defaults host", "", "host", config.ExecutionHost, false},
		{"unset, non-host environment defaults environment", "", "docker", config.ExecutionEnvironment, false},
		{"explicit host wins over environment", "host", "docker", config.ExecutionHost, false},
		{"explicit environment with environment declared", "environment", "docker", config.ExecutionEnvironment, false},
		{"explicit environment with no workflow environment errors", "environment", "", "", true},
		{"explicit environment with explicit host workflow errors", "environment", "host", "", true},
		{"invalid value errors", "docker", "docker", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveExecution(tc.declared, tc.environment)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompileWorkflow_ExecutionDefaultsToWorkflowEnvironment(t *testing.T) {
	plan := buildPlanWithEnvironment(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`}},
		[]nodeStub{{id: "a"}},
		"docker",
	)
	if plan.Run[0].Execution != config.ExecutionEnvironment {
		t.Errorf("Execution = %q, want %q", plan.Run[0].Execution, config.ExecutionEnvironment)
	}
}

func TestCompileWorkflow_ExecutionDefaultsToHostWhenNoEnvironment(t *testing.T) {
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`}},
		[]nodeStub{{id: "a"}},
	)
	if plan.Run[0].Execution != config.ExecutionHost {
		t.Errorf("Execution = %q, want %q", plan.Run[0].Execution, config.ExecutionHost)
	}
}

func TestCompileWorkflow_ExecutionExplicitHostOverridesWorkflowEnvironment(t *testing.T) {
	plan := buildPlanWithEnvironment(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`, execution: config.ExecutionHost}},
		[]nodeStub{{id: "a"}},
		"docker",
	)
	if plan.Run[0].Execution != config.ExecutionHost {
		t.Errorf("Execution = %q, want %q", plan.Run[0].Execution, config.ExecutionHost)
	}
}

func TestCompileWorkflow_ExecutionEnvironmentWithoutWorkflowEnvironmentErrors(t *testing.T) {
	_, err := tryBuildPlanWithEnvironment(
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`, execution: config.ExecutionEnvironment}},
		[]nodeStub{{id: "a"}},
		"",
	)
	if err == nil {
		t.Fatal("expected compile error")
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestEnvironmentExecutor_ForwardsArgvAsPositionalParamsAndExposesOutputsAsEnvVars(t *testing.T) {
	env := config.EnvironmentConfig{
		ID:   "docker",
		Exec: `echo "id=$PLECT_ENV_ID workdir=$PLECT_ENV_WORKDIR"; "$@"`,
	}
	ex := NewEnvironmentExecutor(env, map[string]any{"workdir": "/env/wd"})
	// A shell metacharacter in the target argv must survive untouched: it is
	// forwarded as a single argv element via "$@", never re-parsed by a shell.
	stdout, _, err := ex.Run(context.Background(), ExecRequest{Argv: []string{"echo", "payload;with;semicolons"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := string(stdout)
	if !strings.Contains(got, "id=docker workdir=/env/wd") {
		t.Errorf("output = %q, want PLECT_ENV_* vars exposed", got)
	}
	if !strings.Contains(got, "payload;with;semicolons") {
		t.Errorf("output = %q, want target argv forwarded literally", got)
	}
}

func TestExecutor_RunSetupRoutesEnvironmentExecutionThroughEnvExecutor(t *testing.T) {
	plan := buildPlanWithEnvironment(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`}},
		[]nodeStub{{id: "a"}},
		"docker",
	)
	envSpy := &spyExecutor{stdout: []byte("{}")}
	hostSpy := withSpyExecutor(t)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}, tasks, nil, envSpy); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(envSpy.requests) != 1 {
		t.Fatalf("envSpy requests = %d, want 1: %+v", len(envSpy.requests), envSpy.requests)
	}
	if len(hostSpy.requests) != 0 {
		t.Errorf("host defaultExecutor should not run an environment-plane node, got %+v", hostSpy.requests)
	}
	want := wantExecRequest(`echo '{}'`, "/work/x")
	if envSpy.requests[0].Argv[0] != want.Argv[0] {
		t.Errorf("envSpy request = %+v", envSpy.requests[0])
	}
}

func TestExecutor_RunSetupNilEnvExecutorFailsClosed(t *testing.T) {
	// A node resolved to "environment" (workflow declares one) but no
	// envExecutor supplied (e.g. environment setup itself failed) must fail
	// closed — it must NEVER silently run on host, since the node explicitly
	// asked for the environment's isolation.
	plan := buildPlanWithEnvironment(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`}},
		[]nodeStub{{id: "a"}},
		"docker",
	)
	hostSpy := withSpyExecutor(t)
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), plan.Run, SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}, tasks, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !strings.Contains(err.Error(), "environment executor") {
		t.Errorf("unexpected message: %v", err)
	}
	if len(hostSpy.requests) != 0 {
		t.Errorf("host defaultExecutor must never run an environment-plane node, got %+v", hostSpy.requests)
	}
	st := tasks["a"]
	if st == nil || st.Status != contract.TaskStatusFailed {
		t.Errorf("task state = %+v, want failed", st)
	}
}

func TestExecutor_RunCleanupRoutesEnvironmentExecutionThroughEnvExecutor(t *testing.T) {
	plan := buildPlanWithEnvironment(t,
		[]taskStub{{id: "a", scope: "run", setup: `echo '{}'`, cleanup: `echo cleaned`}},
		[]nodeStub{{id: "a"}},
		"docker",
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	envSpy := &spyExecutor{stdout: []byte("{}")}
	if err := RunCleanup(context.Background(), plan.Run, SessionVars{Name: "x", WorkspaceDirPath: "/work/x"}, tasks, nil, envSpy); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	if len(envSpy.requests) != 1 {
		t.Fatalf("envSpy requests = %d, want 1: %+v", len(envSpy.requests), envSpy.requests)
	}
}
