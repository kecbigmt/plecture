package lang

import (
	"strings"
	"testing"
)

func TestRetiredCleanupInputsAreLoadErrorsThatPointAtTheMigrationGuide(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		code  Code
		layer Layer
	}{
		{
			name: "an effect cleanup inputs_schema",
			body: `
[checkout]
kind  = "effect"
scope = "session"

[checkout.setup]
type    = "exec"
command = "true"

[checkout.cleanup]
type    = "exec"
command = "true"

[checkout.cleanup.inputs_schema]
type = "object"

[checkout.health.alive]
type = "noop"
`,
			code:  CodeFieldUnknown,
			layer: LayerStructural,
		},
		{
			name: "a cleanup.inputs projection",
			body: `
[checkout]
kind  = "effect"
scope = "session"

[checkout.setup]
type    = "exec"
command = "true"

[checkout.cleanup]
type    = "exec"
command = "checkout-tool"
args    = [{ from = "cleanup.inputs.delete_branch", default = false }]

[checkout.health.alive]
type = "noop"
`,
			code:  CodeFromRoot,
			layer: LayerSemantic,
		},
		{
			name: "a cleanup.inputs expression",
			body: `
[checkout]
kind  = "effect"
scope = "session"

[checkout.setup]
type    = "exec"
command = "true"

[checkout.cleanup]
type    = "exec"
command = "checkout-tool"
args    = [{ expr = "string(cleanup.inputs.delete_branch)" }]

[checkout.health.alive]
type = "noop"
`,
			code:  CodeFromRoot,
			layer: LayerSemantic,
		},
		{
			name: "a population session.destroy.inputs table",
			body: `
[standing]
kind     = "workflow"
resource = "query_source"

[[standing.populations]]
name              = "dispatch"
resource_observer = "query_source"
uses              = ["poll"]
poll_every        = "5m"

[standing.populations.query]
scope = "open"

[standing.populations.session.destroy.inputs]
delete_branch = false
`,
			code:  CodeFieldUnknown,
			layer: LayerStructural,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs, err := ParseDefinitionDocument("f.toml", []byte(tt.body))
			if err == nil {
				for _, def := range defs {
					if err = (Validation{}).ValidateDefinition(def); err != nil {
						break
					}
				}
			}
			assertDiagnostic(t, err, tt.code, tt.layer)
			if !strings.Contains(err.Error(), WorkspaceProviderMigrationGuide) {
				t.Errorf("error %q does not point at the migration guide", err)
			}
		})
	}
}
