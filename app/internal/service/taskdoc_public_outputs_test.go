package service

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestRenderInstruction_EvaluatesPublicOutputs(t *testing.T) {
	session := &domain.Session{
		Name:     "review-1",
		Workflow: "wf",
		Nodes: map[string]*contract.TaskState{
			"orchestrator": {Status: contract.TaskStatusProduced, Outputs: map[string]any{"endpoint": "unix:///run/o.sock"}},
		},
	}
	pinDisplayWorkflow(t, config.WorkflowFile{ID: "wf", Outputs: map[string]*lang.Value{
		"endpoint": fromValue("nodes.orchestrator.outputs.endpoint"),
	}}, session)
	doc := config.TaskDocument{ID: "review", Instruction: "Report to {{ workflow.outputs.endpoint }}."}
	got, err := renderInstruction(&config.Config{}, session, doc, nil, "", nil)
	if err != nil {
		t.Fatalf("renderInstruction: %v", err)
	}
	if !strings.Contains(got, "unix:///run/o.sock") {
		t.Fatalf("instruction = %q, want the public output's value", got)
	}
}
