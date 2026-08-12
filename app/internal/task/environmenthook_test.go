package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/state"
)

func TestRunEnvironmentSetup_ProducesOutputs(t *testing.T) {
	env := config.EnvironmentConfig{ID: "docker", Setup: `echo '{"workdir":"/env/wd"}'`}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunEnvironmentSetup(env, EnvironmentHookVars{ResourceID: "res-1", SessionName: "s"}, tasks, nil)
	if err != nil {
		t.Fatalf("RunEnvironmentSetup: %v", err)
	}
	if outputs["workdir"] != "/env/wd" {
		t.Errorf("outputs = %v", outputs)
	}
	st := tasks[contract.EnvironmentPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node state = %+v, want produced", st)
	}
	if st.Scope != contract.TaskScopeSession {
		t.Errorf("scope = %q, want session", st.Scope)
	}
}

// Unlike a provider, an environment may declare no setup at all — it still
// produces the pseudo-node (with empty outputs) so Execution="environment"
// nodes and .Environment.outputs render consistently.
func TestRunEnvironmentSetup_EmptySetupProducesEmptyOutputs(t *testing.T) {
	env := config.EnvironmentConfig{ID: "bare"}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunEnvironmentSetup(env, EnvironmentHookVars{}, tasks, nil)
	if err != nil {
		t.Fatalf("RunEnvironmentSetup: %v", err)
	}
	if len(outputs) != 0 {
		t.Errorf("outputs = %v, want empty", outputs)
	}
	st := tasks[contract.EnvironmentPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node state = %+v, want produced", st)
	}
}

func TestRunEnvironmentSetup_TemplateVars(t *testing.T) {
	env := config.EnvironmentConfig{
		ID:    "docker",
		Setup: `echo "{\"resource\":\"{{.ResourceID}}\",\"workdir\":\"{{.WorktreePath}}\",\"image\":\"{{get .EnvironmentInputs "image"}}\"}"`,
	}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunEnvironmentSetup(env, EnvironmentHookVars{
		ResourceID:        "res-1",
		WorktreePath:      "/work/x",
		EnvironmentInputs: map[string]any{"image": "myimage:latest"},
	}, tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outputs["resource"] != "res-1" || outputs["workdir"] != "/work/x" || outputs["image"] != "myimage:latest" {
		t.Errorf("template vars not rendered: %v", outputs)
	}
}

func TestRunEnvironmentSetup_IdempotentSkip(t *testing.T) {
	env := config.EnvironmentConfig{ID: "docker", Setup: `echo should-not-run >&2; exit 1`}
	tasks := map[string]*contract.TaskState{
		contract.EnvironmentPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workdir": "/env/wd"},
		},
	}
	outputs, err := RunEnvironmentSetup(env, EnvironmentHookVars{}, tasks, nil)
	if err != nil {
		t.Fatalf("produced pseudo-node must short-circuit: %v", err)
	}
	if outputs["workdir"] != "/env/wd" {
		t.Errorf("outputs = %v", outputs)
	}
}

func TestRunEnvironmentSetup_OutputsSchemaEnforced(t *testing.T) {
	env := config.EnvironmentConfig{
		ID:    "docker",
		Setup: `echo '{"workdir":"/env/wd","port":"not-a-number"}'`,
		OutputsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"port": map[string]any{"type": "integer"},
			},
		},
	}
	tasks := map[string]*contract.TaskState{}
	_, err := RunEnvironmentSetup(env, EnvironmentHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected schema violation")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("unexpected message: %v", err)
	}
	st := tasks[contract.EnvironmentPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusFailed {
		t.Errorf("pseudo-node should be failed, got %+v", st)
	}
}

func TestRunEnvironmentSetup_ScriptFailureFailsClosed(t *testing.T) {
	env := config.EnvironmentConfig{ID: "docker", Setup: "exit 7"}
	tasks := map[string]*contract.TaskState{}
	_, err := RunEnvironmentSetup(env, EnvironmentHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected setup failure")
	}
	st := tasks[contract.EnvironmentPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusFailed {
		t.Errorf("pseudo-node should be failed, got %+v", st)
	}
}

func TestRunEnvironmentCleanup_RunsAndMarksClean(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned.txt")
	env := config.EnvironmentConfig{
		ID:      "docker",
		Setup:   "echo '{}'",
		Exec:    `"$@"`,
		Cleanup: `echo "{{.Self.workdir}}" > ` + marker,
	}
	tasks := map[string]*contract.TaskState{
		contract.EnvironmentPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workdir": "/env/wd"},
		},
	}
	if err := RunEnvironmentCleanup(env, EnvironmentHookVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "/env/wd" {
		t.Errorf(".Self.workdir = %q", strings.TrimSpace(string(data)))
	}
	st := tasks[contract.EnvironmentPseudoNodeID]
	if st.Status != contract.TaskStatusCleaned {
		t.Errorf("status = %q, want cleaned", st.Status)
	}
	if st.Outputs["workdir"] != "/env/wd" {
		t.Error("outputs must survive cleanup for .Prev on retry")
	}
}

func TestRunEnvironmentCleanup_NoStateSkips(t *testing.T) {
	env := config.EnvironmentConfig{ID: "docker", Cleanup: "exit 1"}
	if err := RunEnvironmentCleanup(env, EnvironmentHookVars{}, map[string]*contract.TaskState{}, nil); err != nil {
		t.Fatalf("no setup state must skip cleanly: %v", err)
	}
}

func TestRunEnvironmentCleanup_FailureMarksFailedButDoesNotPanic(t *testing.T) {
	env := config.EnvironmentConfig{ID: "docker", Cleanup: "exit 7"}
	tasks := map[string]*contract.TaskState{
		contract.EnvironmentPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workdir": "/env/wd"},
		},
	}
	err := RunEnvironmentCleanup(env, EnvironmentHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if tasks[contract.EnvironmentPseudoNodeID].Status != contract.TaskStatusFailed {
		t.Errorf("status = %q, want failed", tasks[contract.EnvironmentPseudoNodeID].Status)
	}
}

// Node templates can reference the environment pseudo-node's outputs.
func TestRunSetup_ExposesEnvironmentOutputs(t *testing.T) {
	resolved := []Resolved{{
		NodeID:    "echoer",
		Scope:     config.TaskScopeSession,
		Execution: config.ExecutionHost,
		Setup:     `echo "{\"got\":\"{{.Environment.outputs.socket_dir}}\"}"`,
	}}
	tasks := map[string]*contract.TaskState{
		contract.EnvironmentPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"socket_dir": "/env/sockets"},
		},
	}
	if err := RunSetup(context.Background(), resolved, SessionVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	st := tasks["echoer"]
	if st.Outputs["got"] != "/env/sockets" {
		t.Errorf(".Environment.outputs.socket_dir = %v", st.Outputs["got"])
	}
}
