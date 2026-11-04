package service

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestRenderInstruction_ReadsThePublicOutputRecordNotTheProviderPseudoNode(t *testing.T) {
	session := &domain.Session{
		Name:          "review-1",
		PublicOutputs: map[string]any{"endpoint": "unix:///run/o.sock"},
		Nodes: map[string]*contract.TaskState{
			contract.WorkflowPseudoNodeID: {Outputs: map[string]any{"endpoint": "from-provider"}},
		},
	}
	doc := config.TaskDocument{ID: "review", Instruction: "Report to {{ workflow.outputs.endpoint }}."}
	got, err := renderInstruction(&config.Config{}, session, doc, nil, "", nil)
	if err != nil {
		t.Fatalf("renderInstruction: %v", err)
	}
	if !strings.Contains(got, "unix:///run/o.sock") || strings.Contains(got, "from-provider") {
		t.Fatalf("instruction = %q, want the public output's value", got)
	}
}
