package langconfig

import "testing"

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

func TestParseDefinitionDocumentTaskInTOMLDocument(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte(`
[review]
kind              = "task"
description       = "A task declaration with nowhere to keep its instruction"
resource_observer = "issue_pr"
`))
	assertDiagnostic(t, err, CodeTaskInTOMLDocument, LayerStructural)
}

func TestParseDefinitionDocumentEmptyIsLoadError(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte("\n"))
	if err == nil {
		t.Fatal("expected an error for a file with no top-level definition table")
	}
}

func TestParseTaskDocumentValid(t *testing.T) {
	def, err := ParseTaskDocument("document.md", []byte(`+++
[work]
kind              = "task"
description       = "Implement a fix or feature for an issue and create a PR"
resource_observer = "issue_pr"
+++
Resolve the issue at {{ resource.id }}.
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.ID != "work" || def.Kind != KindTask {
		t.Fatalf("unexpected definition: %+v", def)
	}
}

func TestParseTaskDocumentFrontmatterMissing(t *testing.T) {
	_, err := ParseTaskDocument("no-frontmatter.md", []byte("Resolve the issue at {{ resource.id }}.\n"))
	assertDiagnostic(t, err, CodeTaskFrontmatterMissing, LayerStructural)
}

func TestParseTaskDocumentBlockCount(t *testing.T) {
	_, err := ParseTaskDocument("two-blocks.md", []byte(`+++
[work]
kind = "task"

[review]
kind = "task"
+++
body
`))
	assertDiagnostic(t, err, CodeTaskBlockCount, LayerStructural)
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
		{"task-in-toml.invalid.toml", CodeTaskInTOMLDocument},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			_, err := ParseDefinitionDocument(tc.fixture, readConfigLanguageFixture(t, "references/"+tc.fixture))
			assertDiagnostic(t, err, tc.code, LayerStructural)
		})
	}
}

// TestParseTaskDocumentAgainstTasksFixtures runs tasks/ fixtures whose
// violation is frontmatter shape (rather than task-contract content, out of
// scope for this slice) through ParseTaskDocument directly.
func TestParseTaskDocumentAgainstTasksFixtures(t *testing.T) {
	def, err := ParseTaskDocument("document.md", readConfigLanguageFixture(t, "tasks/document.md"))
	if err != nil {
		t.Fatalf("tasks/document.md: unexpected error: %v", err)
	}
	if def.ID != "work" || def.Kind != KindTask {
		t.Fatalf("tasks/document.md: unexpected definition: %+v", def)
	}

	_, err = ParseTaskDocument("no-frontmatter.md", readConfigLanguageFixture(t, "tasks/no-frontmatter.invalid.md"))
	assertDiagnostic(t, err, CodeTaskFrontmatterMissing, LayerStructural)

	_, err = ParseTaskDocument("two-blocks.md", readConfigLanguageFixture(t, "tasks/two-blocks.invalid.md"))
	assertDiagnostic(t, err, CodeTaskBlockCount, LayerStructural)
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
