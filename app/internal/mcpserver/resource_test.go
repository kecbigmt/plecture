package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleResourceStatus_ObservesMatchingDefinition(t *testing.T) {
	setUpConfigHome(t)
	home := os.Getenv("HOME")
	resourcesDir := filepath.Join(home, ".config", "plect", "resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := "[example]\nkind = \"resource_observer\"\nmatch = '^https://example\\.test/'\n" +
		"\n[example.observe]\ntype = \"exec\"\ncommand = \"printf\"\nargs = ['{\"checks_status\":\"SUCCESS\"}']\n"
	if err := os.WriteFile(filepath.Join(resourcesDir, "example.toml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := handleResourceStatus(context.Background(), reqWith(map[string]any{
		"resource_id": "https://example.test/o/r/pull/5",
	}))
	if err != nil {
		t.Fatalf("handleResourceStatus: %v", err)
	}
	out := decodeJSONResult(t, result)
	if out["definition"] != "example" {
		t.Fatalf("definition = %v, want example", out["definition"])
	}
	state, ok := out["state"].(map[string]any)
	if !ok || state["checks_status"] != "SUCCESS" {
		t.Fatalf("state = %v, want checks_status=SUCCESS", out["state"])
	}
}

func TestHandleResourceStatus_NoMatchingDefinition(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleResourceStatus(context.Background(), reqWith(map[string]any{
		"resource_id": "https://unmatched.test/x",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for unmatched resource id")
	}
}

func TestHandleResourceStatus_MissingResourceIDArg(t *testing.T) {
	result, err := handleResourceStatus(context.Background(), reqWith(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing resource_id")
	}
}
