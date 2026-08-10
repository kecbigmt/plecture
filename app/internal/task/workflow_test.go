package task

import (
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
)

func TestCompileWorkflow_DerivesDAGFromInputs(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "coding-claude",
		Nodes: []config.WorkflowNode{
			{
				ID:   "claude",
				Uses: "claude",
				Inputs: map[string]string{
					"session_name": "{{.Nodes.tmux.outputs.session_name}}",
				},
			},
			{ID: "tmux", Uses: "tmux"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"tmux":   {ID: "tmux", Scope: "run", Setup: "true"},
		"claude": {ID: "claude", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if len(plan.Run) != 2 {
		t.Fatalf("Run nodes = %d, want 2", len(plan.Run))
	}
	if plan.Run[0].NodeID != "tmux" || plan.Run[1].NodeID != "claude" {
		t.Errorf("topo order = [%s %s], want [tmux claude]", plan.Run[0].NodeID, plan.Run[1].NodeID)
	}
	if got := plan.Run[1].DependsOn; len(got) != 1 || got[0] != "tmux" {
		t.Errorf("claude.DependsOn = %v, want [tmux]", got)
	}
}

func TestCompileWorkflow_NodeIDDifferentFromTaskID(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "coding-claude",
		Nodes: []config.WorkflowNode{
			{ID: "review_claude", Uses: "claude"},
			{ID: "respond_claude", Uses: "claude"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"claude": {ID: "claude", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if len(plan.Run) != 2 {
		t.Fatalf("expected two instantiations, got %d", len(plan.Run))
	}
	for _, n := range plan.Run {
		if n.TaskID != "claude" {
			t.Errorf("node %q TaskID = %q, want claude", n.NodeID, n.TaskID)
		}
	}
}

func TestCompileWorkflow_UnknownUses(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "broken",
		Nodes: []config.WorkflowNode{
			{ID: "x", Uses: "missing"},
		},
	}
	_, err := CompileWorkflow(wf, map[string]config.TaskDefinition{})
	if err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("expected unknown-task error, got %v", err)
	}
}

func TestCompileWorkflow_UnknownNodeRef(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "broken",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "a", Inputs: map[string]string{
				"x": "{{.Nodes.ghost.outputs.y}}",
			}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("expected unknown-node error, got %v", err)
	}
}

func TestCompileWorkflow_Cycle(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "cycle",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "x", Inputs: map[string]string{"in": "{{.Nodes.b.outputs.k}}"}},
			{ID: "b", Uses: "x", Inputs: map[string]string{"in": "{{.Nodes.a.outputs.k}}"}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"x": {ID: "x", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCompileWorkflow_ScopeViolation(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "bad-scope",
		Nodes: []config.WorkflowNode{
			{ID: "session_node", Uses: "s", Inputs: map[string]string{
				"x": "{{.Nodes.run_node.outputs.y}}",
			}},
			{ID: "run_node", Uses: "r"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"s": {ID: "s", Scope: "session", Setup: "true"},
		"r": {ID: "r", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "session-scoped") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestCompileWorkflow_EmptyNodesRejected(t *testing.T) {
	_, err := CompileWorkflow(config.WorkflowFile{ID: "empty"}, nil)
	if err == nil || !strings.Contains(err.Error(), "declares no nodes") {
		t.Fatalf("expected empty-nodes error, got %v", err)
	}
}

func TestCompileWorkflow_NodeIDDefaultsToUses(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "minimal",
		Nodes: []config.WorkflowNode{
			{Uses: "tmux"},
			{Uses: "claude", Inputs: map[string]string{
				"session_name": "{{.Nodes.tmux.outputs.session_name}}",
			}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"tmux":   {ID: "tmux", Scope: "run", Setup: "true"},
		"claude": {ID: "claude", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if len(plan.Run) != 2 || plan.Run[0].NodeID != "tmux" || plan.Run[1].NodeID != "claude" {
		t.Fatalf("expected node ids defaulted from uses, got %+v", plan.Run)
	}
}

func TestCompileWorkflow_NodeWithoutIDOrUsesRejected(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "bad",
		Nodes: []config.WorkflowNode{
			{Inputs: map[string]string{"k": "v"}},
		},
	}
	_, err := CompileWorkflow(wf, map[string]config.TaskDefinition{})
	if err == nil || !strings.Contains(err.Error(), "must declare at least one of") {
		t.Fatalf("expected missing-id/uses error, got %v", err)
	}
}

func TestCompileWorkflow_HyphenInNodeIDRejected(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "bad-id",
		Nodes: []config.WorkflowNode{
			{ID: "slack-thread", Uses: "x"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"x": {ID: "x", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "Go template identifier") {
		t.Fatalf("expected node-id validation error, got %v", err)
	}
}

func TestCompileWorkflow_InputRefSelfRejected(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "self-ref",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "x", Inputs: map[string]string{
				"in": "{{.Nodes.a.outputs.k}}",
			}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"x": {ID: "x", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("expected self-reference error, got %v", err)
	}
}

func TestCompileWorkflow_BlocksAddsReverseEdge(t *testing.T) {
	// teardown blocks tmux → tmux depends on teardown → setup order is
	// teardown, tmux (with cleanup reversed). This is the cascade-overlay use
	// case: an overlay node forces itself before a base node it cannot modify.
	wf := config.WorkflowFile{
		ID: "coding-claude",
		Nodes: []config.WorkflowNode{
			{ID: "tmux", Uses: "tmux"},
			{ID: "teardown", Uses: "teardown", Blocks: []string{"tmux"}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"tmux":     {ID: "tmux", Scope: "run", Setup: "true"},
		"teardown": {ID: "teardown", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if got := ids(plan.Run); len(got) != 2 || got[0] != "teardown" || got[1] != "tmux" {
		t.Fatalf("Run order = %v, want [teardown tmux]", got)
	}
	tmux := plan.Run[1]
	if len(tmux.DependsOn) != 1 || tmux.DependsOn[0] != "teardown" {
		t.Errorf("tmux.DependsOn = %v, want [teardown]", tmux.DependsOn)
	}
}

func TestCompileWorkflow_BlocksUnknownTarget(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "bad-blocks",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "a", Blocks: []string{"ghost"}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "blocks unknown node") {
		t.Fatalf("expected blocks-unknown-node error, got %v", err)
	}
}

func TestCompileWorkflow_BlocksSelfRejected(t *testing.T) {
	wf := config.WorkflowFile{
		ID: "self-block",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "a", Blocks: []string{"a"}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "blocks itself") {
		t.Fatalf("expected blocks-self error, got %v", err)
	}
}

func TestCompileWorkflow_BlocksRunBlockingSessionRejected(t *testing.T) {
	// run "a" blocks session "b" → session b depends on run a → forbidden
	// (cleanup-time reference would dangle).
	wf := config.WorkflowFile{
		ID: "scope-violation",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "a", Blocks: []string{"b"}},
			{ID: "b", Uses: "b"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
		"b": {ID: "b", Scope: "session", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "must not depend") {
		t.Fatalf("expected scope-violation error, got %v", err)
	}
}

func TestCompileWorkflow_BlocksCycle(t *testing.T) {
	// a blocks b (b depends on a), b blocks a (a depends on b) → cycle.
	wf := config.WorkflowFile{
		ID: "block-cycle",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "a", Blocks: []string{"b"}},
			{ID: "b", Uses: "b", Blocks: []string{"a"}},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
		"b": {ID: "b", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestCompileWorkflow_BlocksResolvesByNodeIDNotTaskID(t *testing.T) {
	// Same task instantiated twice under different node ids. blocks must
	// resolve against node id (matching `{{.Nodes.<id>}}` semantics) — using
	// the task id would be ambiguous when the same task appears multiple
	// times in one workflow.
	wf := config.WorkflowFile{
		ID: "node-id-resolution",
		Nodes: []config.WorkflowNode{
			{ID: "early", Uses: "stub", Blocks: []string{"late"}},
			{ID: "late", Uses: "stub"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"stub": {ID: "stub", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if got := ids(plan.Run); len(got) != 2 || got[0] != "early" || got[1] != "late" {
		t.Fatalf("Run order = %v, want [early late]", got)
	}
	// Referencing the task id (when no node carries that id) fails.
	wfBadRef := config.WorkflowFile{
		ID: "task-id-as-blocks",
		Nodes: []config.WorkflowNode{
			{ID: "a", Uses: "stub", Blocks: []string{"stub"}}, // "stub" is the task id, not a node id
			{ID: "b", Uses: "stub"},
		},
	}
	if _, err := CompileWorkflow(wfBadRef, defs); err == nil || !strings.Contains(err.Error(), "blocks unknown node") {
		t.Fatalf("expected blocks-unknown-node when referencing task id, got %v", err)
	}
}

func TestCompileWorkflow_BlocksMixedCycle(t *testing.T) {
	// a depends on b via input ref; b blocks a → adds edge a-depends-on-b
	// already? No — b blocks a means a depends on b. But a already depends on
	// b via input ref. So no cycle here. Flip it: b's input refs a (b depends
	// on a), and a blocks b (b depends on a too — same direction, no cycle).
	// The real mixed cycle: a's input refs b (a depends on b), and a blocks b
	// (b depends on a) → cycle.
	wf := config.WorkflowFile{
		ID: "mixed-cycle",
		Nodes: []config.WorkflowNode{
			{
				ID:   "a",
				Uses: "a",
				Inputs: map[string]string{
					"x": "{{.Nodes.b.outputs.k}}",
				},
				Blocks: []string{"b"},
			},
			{ID: "b", Uses: "b"},
		},
	}
	defs := map[string]config.TaskDefinition{
		"a": {ID: "a", Scope: "run", Setup: "true"},
		"b": {ID: "b", Scope: "run", Setup: "true"},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error from mixed input-ref + blocks, got %v", err)
	}
}

func TestCompileWorkflow_BlocksIdempotentWithExistingDep(t *testing.T) {
	// claude already depends on tmux via input ref; if tmux also blocks claude
	// (redundantly), no duplicate edge should appear.
	wf := config.WorkflowFile{
		ID: "redundant",
		Nodes: []config.WorkflowNode{
			{ID: "tmux", Uses: "tmux", Blocks: []string{"claude"}},
			{
				ID:   "claude",
				Uses: "claude",
				Inputs: map[string]string{
					"session_name": "{{.Nodes.tmux.outputs.session_name}}",
				},
			},
		},
	}
	defs := map[string]config.TaskDefinition{
		"tmux":   {ID: "tmux", Scope: "run", Setup: "true"},
		"claude": {ID: "claude", Scope: "run", Setup: "true"},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	claude := plan.Run[1]
	if len(claude.DependsOn) != 1 || claude.DependsOn[0] != "tmux" {
		t.Errorf("claude.DependsOn = %v, want [tmux] (deduped)", claude.DependsOn)
	}
}

func TestRenderInputs_RendersAgainstDeps(t *testing.T) {
	deps := map[string]map[string]any{
		"tmux": {"session_name": "abc"},
	}
	out, err := RenderInputs(map[string]string{
		"session_name": "{{.Nodes.tmux.outputs.session_name}}",
		"branch":       "{{.Branch}}",
		"workdir":      "{{.Workflow.outputs.workdir}}",
	}, deps, map[string]any{"workdir": "/tmp/wd"}, SessionVars{Branch: "main"})
	if err != nil {
		t.Fatalf("RenderInputs: %v", err)
	}
	if got := out["session_name"]; got != "abc" {
		t.Errorf("session_name = %v, want abc", got)
	}
	if got := out["branch"]; got != "main" {
		t.Errorf("branch = %v, want main", got)
	}
	if got := out["workdir"]; got != "/tmp/wd" {
		t.Errorf("workdir = %v, want /tmp/wd", got)
	}
}
