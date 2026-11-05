package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
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

func TestMutableOutputKeys_WorkspaceDirIsAnOrdinaryKey(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"workspace_dir": map[string]any{"type": "string", "mutable": true},
		},
	}
	keys, err := MutableOutputKeys(schema, "")
	if err != nil {
		t.Fatalf("MutableOutputKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "workspace_dir" {
		t.Errorf("keys = %v, want [workspace_dir]: no output name is reserved", keys)
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

// The `mutable` annotation must not break JSON Schema compilation — unknown
// keywords are ignored by the validator.
func TestCompileSchema_ToleratesMutableAnnotation(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pr_state": map[string]any{"type": "string", "mutable": true},
		},
	}
	compiled, err := lang.CompileSchema(schema, "", "plect:test:outputs")
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
