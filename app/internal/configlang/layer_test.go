package configlang

import "testing"

func defOf(id string, kind Kind, body map[string]any) *Definition {
	return &Definition{ID: id, Kind: kind, Body: body, File: id + ".toml"}
}

func TestMergeLayerWholeDefinitionKindReplaces(t *testing.T) {
	shallower := []*Definition{defOf("runtime", KindEffect, map[string]any{"scope": "run"})}
	deeper := []*Definition{defOf("runtime", KindEffect, map[string]any{"scope": "session"})}
	merged, err := MergeLayer(shallower, deeper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := findByID(merged, "runtime")
	if got == nil || got.Body["scope"] != "session" {
		t.Fatalf("expected the deeper definition to replace the shallower one, got %+v", got)
	}
}

func TestMergeLayerDifferentKindIsIDDuplicate(t *testing.T) {
	shallower := []*Definition{defOf("goal_review", KindEffect, nil)}
	deeper := []*Definition{defOf("goal_review", KindWorkflow, nil)}
	_, err := MergeLayer(shallower, deeper)
	assertDiagnostic(t, err, CodeIDDuplicate, LayerSemantic)
}

func TestMergeLayerWorkflowCascadeAddsNewField(t *testing.T) {
	shallower := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"workspace_provider": "worktree",
	})}
	deeper := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"name": "cascade overlay",
	})}
	merged, err := MergeLayer(shallower, deeper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := findByID(merged, "review_session")
	if got.Body["workspace_provider"] != "worktree" || got.Body["name"] != "cascade overlay" {
		t.Fatalf("expected both fields to survive the merge, got %+v", got.Body)
	}
}

func TestMergeLayerWorkflowCascadeAppendsNodes(t *testing.T) {
	shallower := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"nodes": []any{map[string]any{"uses": "pane"}},
	})}
	deeper := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"nodes": []any{map[string]any{"uses": "runtime"}},
	})}
	merged, err := MergeLayer(shallower, deeper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nodes := findByID(merged, "review_session").Body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("expected nodes to append across layers, got %v", nodes)
	}
}

func TestMergeLayerWorkflowCascadeRejectsFieldRedeclare(t *testing.T) {
	shallower := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"workspace_provider": "worktree",
	})}
	deeper := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"workspace_provider": "other",
	})}
	if _, err := MergeLayer(shallower, deeper); err == nil {
		t.Fatal("expected an error: a cascade layer may not redeclare a field the shallower layer set")
	}
}

func TestMergeLayerWorkflowCascadeReplacesClocksWholesale(t *testing.T) {
	shallower := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"tick": map[string]any{"heartbeat": "15m"},
	})}
	deeper := []*Definition{defOf("review_session", KindWorkflow, map[string]any{
		"tick": map[string]any{"heartbeat": "5m"},
	})}
	merged, err := MergeLayer(shallower, deeper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tick := findByID(merged, "review_session").Body["tick"].(map[string]any)
	if tick["heartbeat"] != "5m" {
		t.Fatalf("expected the deeper tick table to replace the shallower one wholesale, got %v", tick)
	}
}

func findByID(defs []*Definition, id string) *Definition {
	for _, d := range defs {
		if d.ID == id {
			return d
		}
	}
	return nil
}
