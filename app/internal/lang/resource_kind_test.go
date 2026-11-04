package lang

import (
	"strings"
	"testing"
)

func TestParseDefinitionDocumentAcceptsKindResource(t *testing.T) {
	defs, err := ParseDefinitionDocument("f.toml", []byte(`
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<number>\d+)$'
name  = { from = "match.number" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Kind != KindResource {
		t.Fatalf("defs = %+v, want one definition of kind %q", defs, KindResource)
	}
}

func TestParseDefinitionDocumentRejectsKindResourceObserverAndPointsAtTheMigrationGuide(t *testing.T) {
	_, err := ParseDefinitionDocument("f.toml", []byte(`
[issue]
kind  = "resource_observer"
match = '^x$'
`))
	assertDiagnostic(t, err, CodeKindUnknown, LayerStructural)
	if !strings.Contains(err.Error(), "docs/migrations/resource-kind-migration.md") {
		t.Errorf("error %q does not point at the migration guide", err)
	}
}

// A resource builds its instance name from its own captures and from nothing
// else, so a root a name cannot observe, or a capture the match does not
// declare, is a load error rather than an empty name at run time.
func TestResourceNameContracts(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Code
	}{
		{
			name: "a name projecting a declared capture",
			src: `[r]
kind  = "resource"
match = '^x/(?P<owner>[^/]+)$'
name  = { from = "match.owner" }
`,
		},
		{
			name: "a name computing over declared captures",
			src: `[r]
kind  = "resource"
match = '^x/(?P<owner>[^/]+)/(?P<repo>[^/]+)$'
name  = { expr = "match.owner + '-' + match.repo" }
`,
		},
		{
			name: "a name projecting an undeclared capture",
			src: `[r]
kind  = "resource"
match = '^x/(?P<owner>[^/]+)$'
name  = { from = "match.repo" }
`,
			want: CodeFromPath,
		},
		{
			name: "a name reading a root outside its surface",
			src: `[r]
kind  = "resource"
match = '^x/(?P<owner>[^/]+)$'
name  = { from = "session.name" }
`,
			want: CodeFromRoot,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := ParseDefinitionDocument("f.toml", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			err = Validation{}.ValidateDefinition(defs[0])
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			wantDiag(t, err, tc.want, LayerSemantic)
		})
	}
}

func TestMatchNameDerivesTheNameFromCaptures(t *testing.T) {
	defs, err := ParseDefinitionDocument("f.toml", []byte(`
[r]
kind  = "resource"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
name  = { expr = "match.project + '-issue-' + match.number" }
`))
	if err != nil {
		t.Fatal(err)
	}
	name, err := ParseValue(defs[0].Body["name"], ClassData, Position{File: "f.toml", Path: "r.name"})
	if err != nil {
		t.Fatal(err)
	}
	match := defs[0].Body["match"].(string)
	got, matched, err := MatchName(match, name, "https://forge.example/acme/issues/42")
	if err != nil || !matched || got != "acme-issue-42" {
		t.Errorf("MatchName = (%q, %v, %v), want (acme-issue-42, true, nil)", got, matched, err)
	}
	_, matched, err = MatchName(match, name, "https://elsewhere.example/acme/issues/42")
	if err != nil || matched {
		t.Errorf("a non-matching id = (matched %v, err %v), want an unmatched, error-free result", matched, err)
	}
}

const resourceTaskContext = `[issue_pr]
kind  = "resource"
match = '^https://forge\.example/(?P<number>\d+)$'
name  = { from = "match.number" }

[issue_pr.observe]
type = "exec"
bin  = "github-issue-pr"
args = ["observe"]

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
revision = { type = "string" }

[reviewer]
kind = "workflow"
`

func resolveTaskAgainstResources(t *testing.T, src string) error {
	t.Helper()
	context, err := ParseDefinitionDocument("context.toml", []byte(resourceTaskContext))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := ParseDefinitionDocument("task.toml", []byte(src))
	if err != nil {
		return err
	}
	def := defs[0]
	if err := resolveTaskInstructions(def, "."); err != nil {
		return err
	}
	v := Validation{From: Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath}}
	if err := v.ValidateDefinition(def); err != nil {
		return err
	}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(context, def)}}, nil)
	return v.ValidateTaskContracts(def, registry)
}

func TestTaskResourceFieldNamesAResource(t *testing.T) {
	err := resolveTaskAgainstResources(t, `[work]
kind         = "task"
resource     = "issue_pr"
instructions = [{ text = "Resolve {{ resource.id }}." }]

[work.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskResourceFieldRejectsADeclarationThatIsNotAResource(t *testing.T) {
	err := resolveTaskAgainstResources(t, `[work]
kind         = "task"
resource     = "reviewer"
instructions = [{ text = "Resolve {{ resource.id }}." }]
`)
	wantDiag(t, err, CodeKindMismatch, LayerSemantic)
}

func TestTaskRejectsTheRetiredResourceObserverField(t *testing.T) {
	err := resolveTaskAgainstResources(t, `[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve {{ resource.id }}." }]
`)
	wantDiag(t, err, CodeFieldUnknown, LayerStructural)
}
