package task

import (
	"context"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/publicoutput"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func publicOutputWorkflow() (config.WorkflowFile, map[string]config.TaskDefinition) {
	wf := config.WorkflowFile{
		ID:      "review",
		Outputs: map[string]*lang.Value{"endpoint": fromValue("nodes.orchestrator.outputs.endpoint")},
		Nodes: []config.WorkflowNode{
			{ID: "relay", Uses: "relay", Inputs: map[string]*lang.Value{"peer": fromValue("workflow.outputs.endpoint")}},
			{ID: "orchestrator", Uses: "orchestrator"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"relay":        {ID: "relay", Scope: "run", Setup: shellStub("relay-setup")},
		"orchestrator": {ID: "orchestrator", Scope: "run", Setup: shellStub("orchestrator-setup")},
	}
	return wf, defs
}

func TestCompileWorkflow_NodeReadingAPublicOutputDependsOnItsProducers(t *testing.T) {
	wf, defs := publicOutputWorkflow()
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if len(plan.Run) != 2 || plan.Run[0].NodeID != "orchestrator" || plan.Run[1].NodeID != "relay" {
		t.Fatalf("order = %+v, want the producer before its consumer", plan.Run)
	}
	if got := plan.Run[1].DependsOn; len(got) != 1 || got[0] != "orchestrator" {
		t.Fatalf("relay.DependsOn = %v, want [orchestrator]", got)
	}
}

func TestCompileWorkflow_PublicOutputOfAnUnknownNodeRejected(t *testing.T) {
	wf, defs := publicOutputWorkflow()
	wf.Outputs["ghost"] = fromValue("nodes.ghost.outputs.x")
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("CompileWorkflow = %v, want an unknown-node error naming ghost", err)
	}
}

func TestCompileWorkflow_NodeReadingAPublicOutputItProducesIsRejected(t *testing.T) {
	wf, defs := publicOutputWorkflow()
	wf.Outputs["endpoint"] = fromValue("nodes.relay.outputs.endpoint")
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "reads its own outputs") {
		t.Fatalf("CompileWorkflow = %v, want a self-read error", err)
	}
}

func TestCompileWorkflow_CycleThroughAPublicOutputRejected(t *testing.T) {
	wf, defs := publicOutputWorkflow()
	wf.Nodes[1].Inputs = map[string]*lang.Value{"peer": fromValue("nodes.relay.outputs.x")}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("CompileWorkflow = %v, want a cycle error", err)
	}
}

func TestRunSetup_NodeInputReadsPublicOutputFromTheProducedNode(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
		"orchestrator-setup": `{"endpoint":"unix:///run/o.sock"}`,
	}})
	wf, defs := publicOutputWorkflow()
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	tasks := map[string]*contract.TaskState{}
	vars := SessionVars{PublicOutputs: publicoutput.Declaration{Bindings: wf.Outputs}}
	if err := RunSetup(context.Background(), plan.UpOrder(), vars, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if got := tasks["relay"].Inputs["peer"]; got != "unix:///run/o.sock" {
		t.Fatalf("relay input peer = %v, want the public output's value", got)
	}
}

func TestRunSetup_PublicOutputBeforeItsProducerRunsFailsTheConsumer(t *testing.T) {
	withScriptedExecutor(t, &scriptedExecutor{})
	wf, defs := publicOutputWorkflow()
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	tasks := map[string]*contract.TaskState{}
	vars := SessionVars{PublicOutputs: publicoutput.Declaration{Bindings: wf.Outputs}}
	err = RunSetup(context.Background(), []Resolved{plan.Run[1]}, vars, tasks, nil)
	if err == nil || !strings.Contains(err.Error(), "peer") {
		t.Fatalf("RunSetup = %v, want the unresolved public output reported against the input", err)
	}
}

func TestExecuteTaskSetup_SeesPublicOutputsNotTheProviderPseudoNode(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	def := config.TaskDefinition{ID: "review", Scope: "session", Setup: &lang.Action{
		Type:   lang.ActionShell,
		Script: `echo "$branch"`,
		Bind:   map[string]*lang.Value{"branch": fromValue("workflow.outputs.branch")},
	}}
	r := resolveDef(t, def, "review#1")
	nodes := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {Status: contract.TaskStatusProduced, Outputs: map[string]any{"branch": "from-provider"}},
		"checkout":                    {Status: contract.TaskStatusProduced, Outputs: map[string]any{"ref": "feat/x"}},
	}
	vars := SessionVars{PublicOutputs: publicoutput.Declaration{Bindings: map[string]*lang.Value{"branch": fromValue("nodes.checkout.outputs.ref")}}}
	if _, err := ExecuteTaskSetup(context.Background(), r, nil, vars, nodes); err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if len(exec.bindings) != 1 || !strings.Contains(exec.bindings[0], "feat/x") || strings.Contains(exec.bindings[0], "from-provider") {
		t.Fatalf("bindings = %v, want the public output's value and not the provider's", exec.bindings)
	}
}
