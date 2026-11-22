package lang

import (
	"errors"
	"path/filepath"
	"testing"
)

// repeatedLoads is how often each case is loaded. A walk that takes its first
// error from Go's randomized map order disagrees with itself long before this
// many loads, while the run stays fast.
const repeatedLoads = 50

// TestLoadReportsTheFirstDiagnosticInKeyOrder loads, again and again, one
// document that breaks one rule at three sibling keys. Every load must report
// the diagnostic of the key that sorts first.
func TestLoadReportsTheFirstDiagnosticInKeyOrder(t *testing.T) {
	fixtureRoot := filepath.Join(repoRoot(t), "testdata", "config-language")
	context := fixtureContextDefs(t, fixtureRoot)
	cases := []struct {
		name string
		// path matters to the load pipeline: a workflow declaring populations
		// is user-owned only under workflows/populations.
		path     string
		body     string
		wantCode Code
		wantPath string
		// wantReason is the reason of a diagnostic whose position every
		// offending key shares.
		wantReason string
	}{
		{
			name: "a workflow node with unknown fields",
			path: "workflows/node.toml",
			body: `
[session]
kind     = "workflow"
resource = "issue_pr"

[[session.nodes]]
id    = "first"
uses  = "pane"
gamma = 1
alpha = 1
beta  = 1
`,
			wantCode: CodeFieldUnknown,
			wantPath: "session.nodes[0].alpha",
		},
		{
			name: "one file with three tables lacking kind",
			path: "effects/kindless.toml",
			body: `
[gamma]
scope = "run"

[alpha]
scope = "run"

[beta]
scope = "run"
`,
			wantCode: CodeKindMissing,
			wantPath: "alpha",
		},
		{
			name: "shell bind values each declaring both from and expr",
			path: "effects/bind.toml",
			body: `
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type   = "shell"
script = "true"

[runtime.setup.bind]
gamma = { from = "session.name", expr = "1" }
alpha = { from = "session.name", expr = "1" }
beta  = { from = "session.name", expr = "1" }

[runtime.health.alive]
type = "noop"
`,
			wantCode: CodeValueFromAndExpr,
			wantPath: "runtime.setup.bind.alpha",
		},
		{
			name: "a tagged value carrying unknown keys",
			path: "effects/tagged.toml",
			body: `
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type    = "exec"
command = "true"
args    = [{ expr = "'x'", gamma = 1, alpha = 1, beta = 1 }]

[runtime.health.alive]
type = "noop"
`,
			wantCode:   CodeValueTagUnknown,
			wantPath:   "runtime.setup.args[0]",
			wantReason: `"alpha" is not part of the { expr } form`,
		},
		{
			name: "json operand fields each declaring both from and expr",
			path: "effects/json.toml",
			body: `
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type    = "exec"
command = "true"

[runtime.setup.stdin.json]
gamma = { from = "session.name", expr = "1" }
alpha = { from = "session.name", expr = "1" }
beta  = { from = "session.name", expr = "1" }

[runtime.health.alive]
type = "noop"
`,
			wantCode: CodeValueFromAndExpr,
			wantPath: "runtime.setup.stdin.alpha",
		},
		{
			name: "a contract document carrying tagged values",
			path: "effects/contract.toml",
			body: `
[runtime]
kind  = "effect"
scope = "run"

[runtime.inputs_schema.properties]
gamma = { from = "session.name" }
alpha = { from = "session.name" }
beta  = { from = "session.name" }

[runtime.setup]
type    = "exec"
command = "true"

[runtime.health.alive]
type = "noop"
`,
			wantCode: CodeValueTagSurface,
			wantPath: "runtime.inputs_schema.properties.alpha",
		},
		{
			name: "an instructions element with unknown fields",
			path: "tasks/instructions.toml",
			body: `
[work]
kind         = "task"
resource     = "issue_pr"
instructions = [{ text = "x", gamma = 1, alpha = 1, beta = 1 }]
`,
			wantCode: CodeFieldUnknown,
			wantPath: "work.instructions[0].alpha",
		},
		{
			name: "a query action reading undeclared inputs",
			path: "resources/query-inputs.toml",
			body: `
[source]
kind = "resource"

[source.query.inputs_schema]
type = "object"

[source.query.item_schema]
type     = "object"
required = ["resource"]

[source.query.item_schema.properties]
resource = { type = "string" }

[source.query.poll]
type   = "shell"
script = "true"

[source.query.poll.bind]
gamma = { from = "inputs.gamma" }
alpha = { from = "inputs.alpha" }
beta  = { from = "inputs.beta" }
`,
			wantCode: CodeFromPath,
			wantPath: "source.query.poll.bind.alpha",
		},
		{
			name: "query item properties duplicating state facts",
			path: "resources/query-state.toml",
			body: `
[source]
kind = "resource"

[source.state_schema]
type = "object"

[source.state_schema.properties]
gamma = { type = "string" }
alpha = { type = "string" }
beta  = { type = "string" }

[source.query.inputs_schema]
type = "object"

[source.query.item_schema]
type     = "object"
required = ["resource"]

[source.query.item_schema.properties]
resource = { type = "string" }
gamma    = { type = "string" }
alpha    = { type = "string" }
beta     = { type = "string" }

[source.query.poll]
type    = "exec"
command = "true"
`,
			wantCode: CodeFromPath,
			wantPath: "source.query.item_schema.properties.alpha",
		},
		{
			name: "population session inputs projecting undeclared item properties",
			path: "workflows/populations-order.toml",
			body: `
[dispatcher]
kind     = "workflow"
resource = "query_source"

[[dispatcher.populations]]
name       = "dispatch"
uses       = ["poll"]
poll_every = "5m"

[dispatcher.populations.query]
scope = "open"

[dispatcher.populations.session.inputs]
gamma = { from = "item.gamma" }
alpha = { from = "item.alpha" }
beta  = { from = "item.beta" }
`,
			wantCode: CodeFromPath,
			wantPath: "dispatcher.populations.[0].session.inputs.alpha",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(fixtureRoot, filepath.FromSlash(tc.path))
			for i := range repeatedLoads {
				err := nativeLoad(path, tc.body, context, fixtureRoot)
				var d *Diagnostic
				if !errors.As(err, &d) {
					t.Fatalf("load %d: want %s at %s, got %v", i, tc.wantCode, tc.wantPath, err)
				}
				if d.Code != tc.wantCode || d.Pos.Path != tc.wantPath {
					t.Fatalf("load %d: want %s at %s, got %v", i, tc.wantCode, tc.wantPath, err)
				}
				if tc.wantReason != "" && d.Reason != tc.wantReason {
					t.Fatalf("load %d: want reason %q, got %q", i, tc.wantReason, d.Reason)
				}
			}
		})
	}
}
