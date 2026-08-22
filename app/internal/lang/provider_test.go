package lang

import "testing"

// A provider builds its session name from its own captures and reads its own
// declared outputs, so a key that neither declares is a load error rather
// than an empty value at dispatch or teardown.
func TestProviderContracts(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Code
	}{
		{
			name: "a name projecting a declared capture",
			src: `[p]
kind  = "workspace_provider"
match = '^x/(?P<owner>[^/]+)$'
name  = { from = "match.owner" }

[p.setup]
type = "exec"
bin  = "okf-bundle"
`,
		},
		{
			name: "a name computing over declared captures",
			src: `[p]
kind  = "workspace_provider"
match = '^x/(?P<owner>[^/]+)/(?P<repo>[^/]+)$'
name  = { expr = "match.owner + '/' + match.repo" }

[p.setup]
type = "exec"
bin  = "okf-bundle"
`,
		},
		{
			name: "a name projecting an undeclared capture",
			src: `[p]
kind  = "workspace_provider"
match = '^x/(?P<owner>[^/]+)$'
name  = { from = "match.repo" }

[p.setup]
type = "exec"
bin  = "okf-bundle"
`,
			want: CodeFromPath,
		},
		{
			name: "a name computing over an undeclared capture",
			src: `[p]
kind  = "workspace_provider"
match = '^x/(?P<owner>[^/]+)$'
name  = { expr = "match.owner + '-' + match.number" }

[p.setup]
type = "exec"
bin  = "okf-bundle"
`,
			want: CodeFromPath,
		},
		{
			name: "a cleanup reading a declared output",
			src: `[p]
kind = "workspace_provider"

[p.setup]
type = "exec"
bin  = "okf-bundle"

[p.cleanup]
type = "exec"
bin  = "okf-bundle"
args = [{ from = "self.outputs.workspace_dir" }]

[p.outputs_schema]
type = "object"

[p.outputs_schema.properties]
workspace_dir = { type = "string" }
`,
		},
		{
			name: "a cleanup reading an undeclared output",
			src: `[p]
kind = "workspace_provider"

[p.setup]
type = "exec"
bin  = "okf-bundle"

[p.cleanup]
type = "exec"
bin  = "okf-bundle"
args = [{ from = "self.outputs.branch" }]

[p.outputs_schema]
type = "object"

[p.outputs_schema.properties]
workspace_dir = { type = "string" }
`,
			want: CodeFromPath,
		},
		{
			name: "a cleanup computing over an undeclared output",
			src: `[p]
kind = "workspace_provider"

[p.setup]
type = "exec"
bin  = "okf-bundle"

[p.cleanup]
type   = "shell"
script = "true"

[p.cleanup.bind]
label = { expr = "'dir-' + self.outputs.branch" }

[p.outputs_schema]
type = "object"

[p.outputs_schema.properties]
workspace_dir = { type = "string" }
`,
			want: CodeFromPath,
		},
		{
			// A provider declaring no outputs contract has nothing to check a
			// key against; demanding one is a different rule than this.
			name: "a cleanup reading an output where no contract is declared",
			src: `[p]
kind = "workspace_provider"

[p.setup]
type = "exec"
bin  = "okf-bundle"

[p.cleanup]
type = "exec"
bin  = "okf-bundle"
args = [{ from = "self.outputs.branch" }]
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs, err := ParseDefinitionDocument("test.toml", []byte(tt.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			v := Validation{
				From:        Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath},
				Executables: NewExecutableRegistry(PluginExecutables{Alias: fixtureAlias, Path: fixturePath, Names: fixtureExecutables}),
			}
			got := v.ValidateProviderContracts(defs[0])
			if tt.want == "" {
				if got != nil {
					t.Fatalf("expected this to load, but: %v", got)
				}
				return
			}
			wantDiag(t, got, tt.want, LayerSemantic)
		})
	}
}
