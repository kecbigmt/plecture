package lang

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDefinitionDocumentValid(t *testing.T) {
	defs, err := ParseDefinitionDocument("relative.toml", []byte(`
[worktree]
kind = "workspace_provider"

[worktree.setup]
type = "exec"
bin  = "github-worktree"

[runtime]
kind  = "effect"
scope = "run"

[review_session]
kind               = "workflow"
workspace_provider = "worktree"

[[review_session.nodes]]
uses = "runtime"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("got %d definitions, want 3", len(defs))
	}
	byID := map[string]*Definition{}
	for _, d := range defs {
		byID[d.ID] = d
	}
	if byID["worktree"] == nil || byID["worktree"].Kind != KindWorkspaceProvider {
		t.Errorf("worktree: %+v", byID["worktree"])
	}
	if byID["runtime"] == nil || byID["runtime"].Kind != KindEffect {
		t.Errorf("runtime: %+v", byID["runtime"])
	}
	if byID["review_session"] == nil || byID["review_session"].Kind != KindWorkflow {
		t.Errorf("review_session: %+v", byID["review_session"])
	}
}

func TestParseDefinitionDocumentKindMissing(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte(`
[runtime]
scope = "run"
`))
	assertDiagnostic(t, err, CodeKindMissing, LayerStructural)
}

func TestParseDefinitionDocumentKindUnknown(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte(`
[sandbox]
kind = "environment"
`))
	assertDiagnostic(t, err, CodeKindUnknown, LayerStructural)
}

func TestParseDefinitionDocumentIDInvalid(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte(`
[local-okf]
kind = "workspace_provider"
`))
	assertDiagnostic(t, err, CodeIDInvalid, LayerStructural)
}

func TestParseDefinitionDocumentTaskIsAnOrdinaryDeclaration(t *testing.T) {
	defs, err := ParseDefinitionDocument("f.toml", []byte(`
[review]
kind              = "task"
description       = "A task declared like any other kind"
resource_observer = "issue_pr"
instruction       = "Resolve the issue."
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 || defs[0].Kind != KindTask {
		t.Fatalf("unexpected definitions: %+v", defs)
	}
}

func TestParseDefinitionDocumentEmptyIsLoadError(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte("\n"))
	if err == nil {
		t.Fatal("expected an error for a file with no top-level definition table")
	}
}

func TestResolveTaskInstructionInline(t *testing.T) {
	defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instruction       = "Resolve the issue at {{ resource.id }}."
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := defs[0]
	if err := resolveTaskInstruction(def, "tasks"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Instruction != "Resolve the issue at {{ resource.id }}." {
		t.Errorf("Instruction = %q", def.Instruction)
	}
}

func TestResolveTaskInstructionBothInlineAndFile(t *testing.T) {
	defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instruction       = "Resolve the issue."
instruction_file  = "work.md"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstruction(defs[0], "tasks")
	assertDiagnostic(t, err, CodeTaskInstructionAndFile, LayerStructural)
}

func TestResolveTaskInstructionFileCrossLayer(t *testing.T) {
	defs, err := ParseDefinitionDocument(filepath.Join("layer", "tasks", "work.toml"), []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instruction_file  = "../../outside.md"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstruction(defs[0], filepath.Join("layer"))
	assertDiagnostic(t, err, CodeTaskInstructionFileCrossLayer, LayerSemantic)
}

func TestResolveTaskInstructionFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instruction_file  = "work.md"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstruction(defs[0], dir)
	assertDiagnostic(t, err, CodeTaskInstructionFileMissing, LayerSemantic)
}

func TestResolveTaskInstructionFileRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "work.md"), []byte("Resolve the issue at {{ resource.id }}.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instruction_file  = "work.md"
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := resolveTaskInstruction(defs[0], dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if defs[0].Instruction != "Resolve the issue at {{ resource.id }}.\n" {
		t.Errorf("Instruction = %q", defs[0].Instruction)
	}
}

// TestParseDefinitionDocumentAgainstReferencesFixtures runs the structural
// discovery fixtures under testdata/config-language/references/ through
// ParseDefinitionDocument directly, on top of the reference-resolution
// fixtures reference_test.go already exercises through the full loader.
func TestParseDefinitionDocumentAgainstReferencesFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		code    Code
	}{
		{"kind-missing.invalid.toml", CodeKindMissing},
		{"kind-unknown.invalid.toml", CodeKindUnknown},
		{"id-invalid.invalid.toml", CodeIDInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			_, err := ParseDefinitionDocument(tc.fixture, readConfigLanguageFixture(t, "references/"+tc.fixture))
			assertDiagnostic(t, err, tc.code, LayerStructural)
		})
	}
}

// TestParseTaskDocumentAgainstTasksFixtures runs the tasks/document.toml
// fixture and its sidecar through the same discovery step DiscoverRoot uses.
func TestParseTaskDocumentAgainstTasksFixtures(t *testing.T) {
	fixtureRoot := filepath.Join(repoRoot(t), "testdata", "config-language")
	path := filepath.Join(fixtureRoot, "tasks", "document.toml")
	defs, err := ParseDefinitionDocument(path, readConfigLanguageFixture(t, "tasks/document.toml"))
	if err != nil {
		t.Fatalf("tasks/document.toml: unexpected error: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != "work" || defs[0].Kind != KindTask {
		t.Fatalf("tasks/document.toml: unexpected definitions: %+v", defs)
	}
	if err := resolveTaskInstruction(defs[0], fixtureRoot); err != nil {
		t.Fatalf("tasks/document.toml: unexpected error resolving instruction: %v", err)
	}
	if defs[0].Instruction == "" {
		t.Fatal("tasks/document.toml: instruction did not resolve from its sidecar")
	}
}

func TestIsValidID(t *testing.T) {
	cases := map[string]bool{
		"worktree":  true,
		"_worktree": true,
		"worktree1": true,
		"local-okf": false,
		"local.okf": false,
		"1worktree": false,
		"":          false,
	}
	for id, want := range cases {
		if got := isValidID(id); got != want {
			t.Errorf("isValidID(%q) = %v, want %v", id, got, want)
		}
	}
}
