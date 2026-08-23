package mcpserver

import (
	"context"
	"testing"
)

func TestHandleWorkflowList_ListsFixtureWorkflow(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleWorkflowList(context.Background(), reqWith(map[string]any{}))
	if err != nil {
		t.Fatalf("handleWorkflowList: %v", err)
	}
	out := decodeJSONResult(t, result)
	workflows, ok := out["workflows"].([]any)
	if !ok {
		t.Fatalf("workflows field is not a list: %v", out["workflows"])
	}
	found := false
	for _, w := range workflows {
		entry, ok := w.(map[string]any)
		if ok && entry["id"] == "plain" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected workflow \"plain\" in list, got %v", workflows)
	}
}

func TestHandleWorkflowShow_ReturnsCompiledDAG(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleWorkflowShow(context.Background(), reqWith(map[string]any{"id": "plain"}))
	if err != nil {
		t.Fatalf("handleWorkflowShow: %v", err)
	}
	out := decodeJSONResult(t, result)
	workflow, ok := out["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("workflow field is not an object: %v", out["workflow"])
	}
	if workflow["id"] != "plain" {
		t.Fatalf("workflow id = %v, want plain", workflow["id"])
	}
}

func TestHandleWorkflowShow_UnknownID(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleWorkflowShow(context.Background(), reqWith(map[string]any{"id": "does-not-exist"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for unknown workflow id")
	}
}

func TestHandleWorkflowShow_MissingIDArg(t *testing.T) {
	result, err := handleWorkflowShow(context.Background(), reqWith(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing id")
	}
}
