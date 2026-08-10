package task

import (
	"testing"

	contract "github.com/kecbigmt/sennit/contracts/state"
)

func TestRenderOutputsTemplate_WorkflowOutputs(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		outputs map[string]any
		want    string
		wantErr bool
	}{
		{
			name:    "satisfied",
			expr:    `{{eq .Workflow.outputs.pr_state "merged"}}`,
			outputs: map[string]any{"pr_state": "merged"},
			want:    "true",
		},
		{
			name:    "not satisfied",
			expr:    `{{eq .Workflow.outputs.pr_state "merged"}}`,
			outputs: map[string]any{"pr_state": "open"},
			want:    "false",
		},
		{
			name:    "missing key is not done",
			expr:    `{{eq .Workflow.outputs.pr_state "merged"}}`,
			outputs: map[string]any{},
			want:    "false",
		},
		{
			name:    "nil outputs is not done",
			expr:    `{{eq .Workflow.outputs.pr_state "merged"}}`,
			outputs: nil,
			want:    "false",
		},
		{
			name:    "compound condition",
			expr:    `{{and (eq .Workflow.outputs.pr_state "merged") (eq .Workflow.outputs.checks_status "passing")}}`,
			outputs: map[string]any{"pr_state": "merged", "checks_status": "passing"},
			want:    "true",
		},
		{
			name:    "parse error",
			expr:    `{{eq .Workflow.outputs.pr_state "merged"`,
			outputs: map[string]any{"pr_state": "merged"},
			wantErr: true,
		},
		{
			name:    "execute error fails closed",
			expr:    `{{eq .Workflow.outputs.pr_state}}`,
			outputs: map[string]any{"pr_state": "merged"},
			wantErr: true,
		},
		{
			name:    "raw output renders as string",
			expr:    `{{.Workflow.outputs.pr_state}}`,
			outputs: map[string]any{"pr_state": "merged"},
			want:    "merged",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderOutputsTemplate(tt.expr, tt.outputs, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderOutputsTemplate_NodeOutputs(t *testing.T) {
	nodes := map[string]*contract.TaskState{
		"task": {
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"finished": "yes", "count": float64(3)},
		},
		"nilstate": nil,
	}
	got, err := RenderOutputsTemplate(`{{eq .Nodes.task.outputs.finished "yes"}}`, nil, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "true" {
		t.Error("expected node-output predicate to hold")
	}

	// JSON float64 normalization: integers render as ints, comparable via eq.
	got, err = RenderOutputsTemplate(`{{eq .Nodes.task.outputs.count 3}}`, nil, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "true" {
		t.Error("expected normalized int comparison to hold")
	}
}
