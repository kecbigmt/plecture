package configlang

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDiscoverRootReadsEveryDefinitionFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "effects/runtime.toml", `
[runtime]
kind  = "effect"
scope = "run"
`)
	mustWrite(t, dir, "workflows/review.toml", `
[review_session]
kind = "workflow"

[[review_session.nodes]]
uses = "runtime"
`)
	mustWrite(t, dir, "task.md", "+++\n[work]\nkind = \"task\"\n+++\nDo it.\n")
	mustWrite(t, dir, "readme.md", "Just prose, no frontmatter.\n")

	defs, err := DiscoverRoot(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("got %d definitions, want 3 (readme.md is a template asset): %v", len(defs), defNames(defs))
	}
}

func TestDiscoverRootSkipsReservedFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "config.toml", `schema_version = 1`)
	mustWrite(t, dir, "runtime.toml", `
[runtime]
kind = "effect"
`)
	defs, err := DiscoverRoot(dir, ReservedFileNames)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != "runtime" {
		t.Fatalf("expected only runtime discovered, got %v", defNames(defs))
	}
}

func TestDiscoverRootCrossFileDuplicateID(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "a.toml", `
[goal_review]
kind = "effect"
`)
	mustWrite(t, dir, "b.toml", `
[goal_review]
kind = "workflow"
`)
	_, err := DiscoverRoot(dir, nil)
	assertDiagnostic(t, err, CodeIDDuplicate, LayerSemantic)
}

func defNames(defs []*Definition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.ID
	}
	return names
}
