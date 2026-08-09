package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
)

func TestMutableOutputKeys_ExtractsAnnotatedKeys(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pr_state": map[string]any{"type": "string", "mutable": true},
			"title":    map[string]any{"type": "string"},
			"checks":   map[string]any{"type": "string", "mutable": true},
			"weird":    "not-a-map",
		},
	}
	keys, err := MutableOutputKeys(schema, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"checks", "pr_state"}; len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestMutableOutputKeys_NoSchemaMeansNothingMutable(t *testing.T) {
	keys, err := MutableOutputKeys(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil", keys)
	}
}

func TestMutableOutputKeys_WorkdirMutableIsLoadError(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"workdir": map[string]any{"type": "string", "mutable": true},
		},
	}
	_, err := MutableOutputKeys(schema, "")
	if err == nil {
		t.Fatal("expected error: workdir is reserved always-immutable")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestMutableOutputKeys_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outputs.json")
	doc := `{"type":"object","properties":{"pr_state":{"type":"string","mutable":true}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := MutableOutputKeys(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "pr_state" {
		t.Errorf("keys = %v, want [pr_state]", keys)
	}
}

// CompileWorkflow must reject a task whose schema declares workdir mutable
// — load-time failure, before any lifecycle command can run against it.
func TestCompileWorkflow_RejectsWorkdirMutableDeclaration(t *testing.T) {
	wf := config.WorkflowFile{
		ID:    "wf",
		Nodes: []config.WorkflowNode{{ID: "bad"}},
	}
	defs := map[string]config.TaskDefinition{
		"bad": {
			ID:    "bad",
			Setup: "echo '{}'",
			OutputsSchema: map[string]any{
				"properties": map[string]any{
					"workdir": map[string]any{"type": "string", "mutable": true},
				},
			},
		},
	}
	_, err := CompileWorkflow(wf, defs)
	if err == nil {
		t.Fatal("expected compile error")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("unexpected message: %v", err)
	}
}

// The `mutable` annotation must not break JSON Schema compilation — unknown
// keywords are ignored by the validator.
func TestCompileSchema_ToleratesMutableAnnotation(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pr_state": map[string]any{"type": "string", "mutable": true},
		},
	}
	compiled, err := CompileSchema(schema, "", "tws:test:outputs")
	if err != nil {
		t.Fatalf("CompileSchema: %v", err)
	}
	if err := compiled.Validate(map[string]any{"pr_state": "open"}); err != nil {
		t.Errorf("validation should pass: %v", err)
	}
	if err := compiled.Validate(map[string]any{"pr_state": 42}); err == nil {
		t.Error("type assertion should still be enforced")
	}
}
