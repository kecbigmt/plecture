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
instructions      = [{ text = "Resolve the issue." }]
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

func TestResolveTaskInstructionsInline(t *testing.T) {
	defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue at {{ resource.id }}." }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := defs[0]
	if err := resolveTaskInstructions(def, "tasks"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Instruction != "Resolve the issue at {{ resource.id }}." {
		t.Errorf("Instruction = %q", def.Instruction)
	}
}

func TestResolveTaskInstructionsJoinsElementsWithABlankLine(t *testing.T) {
	defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "First." }, { text = "Second." }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := resolveTaskInstructions(defs[0], "tasks"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "First.\n\nSecond."; defs[0].Instruction != want {
		t.Errorf("Instruction = %q, want %q", defs[0].Instruction, want)
	}
}

func TestResolveTaskInstructionsElementShapeViolations(t *testing.T) {
	cases := []struct {
		name    string
		element string
	}{
		{"both text and file", `{ text = "Resolve the issue.", file = "work.md" }`},
		{"neither text nor file", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [`+tc.element+`]
`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			err = resolveTaskInstructions(defs[0], "tasks")
			assertDiagnostic(t, err, CodeTaskInstructionElement, LayerStructural)
		})
	}
}

func TestResolveTaskInstructionsElementUnknownField(t *testing.T) {
	defs, err := ParseDefinitionDocument("tasks/work.toml", []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue.", bogus = "x" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstructions(defs[0], "tasks")
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
}

func TestResolveTaskInstructionsFileCrossLayer(t *testing.T) {
	defs, err := ParseDefinitionDocument(filepath.Join("layer", "tasks", "work.toml"), []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ file = "../../outside.md" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstructions(defs[0], filepath.Join("layer"))
	assertDiagnostic(t, err, CodeTaskInstructionFileCrossLayer, LayerSemantic)
}

func TestResolveTaskInstructionsFileRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "etc")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// If an absolute file value were joined onto the declaring directory
	// instead of rejected outright, this file would make the bug invisible:
	// filepath.Join(dir, "/etc/passwd") lands right here and would load.
	if err := os.WriteFile(filepath.Join(nested, "passwd"), []byte("nested, not the real /etc/passwd"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ file = "/etc/passwd" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstructions(defs[0], dir)
	assertDiagnostic(t, err, CodeTaskInstructionFileCrossLayer, LayerSemantic)
}

func TestResolveTaskInstructionsFileEscapesViaSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tasks", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(root, "tasks", "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ file = "link/secret.md" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstructions(defs[0], root)
	assertDiagnostic(t, err, CodeTaskInstructionFileCrossLayer, LayerSemantic)
}

func TestResolveTaskInstructionsFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ file = "work.md" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = resolveTaskInstructions(defs[0], dir)
	assertDiagnostic(t, err, CodeTaskInstructionFileMissing, LayerSemantic)
}

func TestResolveTaskInstructionsFileRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "work.md"), []byte("Resolve the issue at {{ resource.id }}.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "work.toml")
	defs, err := ParseDefinitionDocument(path, []byte(`
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ file = "work.md" }]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := resolveTaskInstructions(defs[0], dir); err != nil {
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
	if err := resolveTaskInstructions(defs[0], fixtureRoot); err != nil {
		t.Fatalf("tasks/document.toml: unexpected error resolving instruction: %v", err)
	}
	if defs[0].Instruction == "" {
		t.Fatal("tasks/document.toml: instruction did not resolve from its sidecar")
	}
}

// preMigrationDocumentInstruction is the instruction body
// tasks/document.md carried below its `+++` frontmatter before the task
// frontmatter document class was retired, byte for byte.
const preMigrationDocumentInstruction = `Resolve the issue at {{ resource.id }}.

Steps:

1. Understand the issue
2. Investigate the relevant code
3. Implement the changes
4. Write and run tests
5. Commit and push, then open a pull request
`

// TestDocumentFixtureConversionIsByteIdentical is the golden comparison the
// migration promises: a single-element instructions array naming a sidecar
// file resolves to exactly the instruction text the retired +++ frontmatter
// document carried, with nothing added, dropped, or reordered.
func TestDocumentFixtureConversionIsByteIdentical(t *testing.T) {
	fixtureRoot := filepath.Join(repoRoot(t), "testdata", "config-language")
	path := filepath.Join(fixtureRoot, "tasks", "document.toml")
	defs, err := ParseDefinitionDocument(path, readConfigLanguageFixture(t, "tasks/document.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveTaskInstructions(defs[0], fixtureRoot); err != nil {
		t.Fatal(err)
	}
	if defs[0].Instruction != preMigrationDocumentInstruction {
		t.Errorf("instruction is not byte-identical to the pre-migration document:\n got: %q\nwant: %q",
			defs[0].Instruction, preMigrationDocumentInstruction)
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
