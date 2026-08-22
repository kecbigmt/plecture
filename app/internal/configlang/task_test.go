package configlang

import "testing"

// taskContext is the observer and workflow a task document under test is
// written against, standing in for the rest of the layer.
const taskContext = `[issue_pr]
kind  = "resource_observer"
match = '^https://github\.com/[^/]+/[^/]+/(issues|pull)/[0-9]+$'

[issue_pr.observe]
type = "exec"
bin  = "github-issue-pr"
args = ["observe"]

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
resource_kind = { type = "string" }
checks_status = { type = "string" }
revision      = { type = "string" }

[goal_reviewer]
kind = "workflow"
`

// resolveTaskDocument loads one task document against taskContext and runs
// both validation passes, so a test states only the document it is about.
func resolveTaskDocument(t *testing.T, src string) error {
	t.Helper()
	context, err := ParseDefinitionDocument("context.toml", []byte(taskContext))
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseTaskDocument("task.md", []byte(src))
	if err != nil {
		return err
	}
	v := Validation{
		From:        Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath},
		Executables: NewExecutableRegistry(PluginExecutables{Alias: fixtureAlias, Path: fixturePath, Names: fixtureExecutables}),
	}
	if err := v.ValidateDefinition(def); err != nil {
		return err
	}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(context, def)}}, nil)
	return v.ValidateTaskContracts(def, registry)
}

func TestValidateTaskContractsRejectsAnUndeclaredKey(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a completion check on a key the observer does not publish",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.done_when]
all = [{ check = "resource.state.mergeability", in = ["clean"] }]
+++
Resolve {{ resource.id }}.
`,
		},
		{
			name: "a completion check on state this document does not keep",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.done_when]
all = [{ check = "self.state.verdict_revision", ne = "" }]
+++
Resolve {{ resource.id }}.
`,
		},
		{
			name: "a computed leaf reading an unpublished key",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.state_schema]
type = "object"

[work.state_schema.properties]
verdict_revision = { type = "string" }

[work.done_when]
all = [{ expr = "self.state.verdict_revision == resource.state.head_sha" }]
+++
Resolve {{ resource.id }}.
`,
		},
		{
			name: "an instruction body projecting an unpublished key",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"
+++
Resolve {{ resource.id }} at {{ resource.state.head_sha }}.
`,
		},
		{
			name: "a chain fact reading an unpublished key",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[[work.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[work.chains.when]
all = [{ check = "resource.state.head_sha", ne = "" }]
+++
Resolve {{ resource.id }}.
`,
		},
		{
			name: "a chain input projecting an unpublished key",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[[work.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[work.chains.when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]

[work.chains.inputs]
head = { from = "resource.state.head_sha" }
+++
Resolve {{ resource.id }}.
`,
		},
		{
			name: "a chain waiting on a judge this document never declares",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.done_when]
all = [{ judge = "the work is done", id = "ac-met" }]

[[work.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[work.chains.when]
all = [{ judge_pending = "solves" }]
+++
Resolve {{ resource.id }}.
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantDiag(t, resolveTaskDocument(t, tc.src), CodeFromPath, LayerSemantic)
		})
	}
}

func TestValidateTaskContractsResolvesEveryDeclaredKey(t *testing.T) {
	src := `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.state_schema]
type = "object"

[work.state_schema.properties]
verdict_revision = { type = "string" }

[work.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
  { judge = "the work is done", id = "ac-met" },
]

[[work.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[work.chains.when]
all = [
  { check = "resource.state.checks_status", in = ["SUCCESS"] },
  { judge_pending = "ac-met" },
]

[work.chains.inputs]
work_session = { from = "task.session" }
revision     = { from = "resource.state.revision" }
+++
Review {{ resource.id }} at {{ resource.state.revision }}, last judged at {{ self.state.verdict_revision }}.
`
	if err := resolveTaskDocument(t, src); err != nil {
		t.Fatalf("every key this document reads is declared: %v", err)
	}
}

func TestValidateTaskContractsRequiresAnObserver(t *testing.T) {
	src := `+++
[work]
kind = "task"

[work.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
+++
Resolve {{ resource.id }}.
`
	wantDiag(t, resolveTaskDocument(t, src), CodeFieldRequired, LayerStructural)
}

func TestValidateTaskContractsRejectsAnObserverOfAnotherKind(t *testing.T) {
	src := `+++
[work]
kind              = "task"
resource_observer = "goal_reviewer"
+++
Resolve {{ resource.id }}.
`
	wantDiag(t, resolveTaskDocument(t, src), CodeKindMismatch, LayerSemantic)
}

// An observer keeping its contract in a separate file leaves nothing to
// resolve against here, and a document is not rejected for a schema this
// pass cannot read.
func TestValidateTaskContractsSkipsAnUnreadableContract(t *testing.T) {
	context, err := ParseDefinitionDocument("context.toml", []byte(`[filed]
kind              = "resource_observer"
state_schema_file = "state.schema.json"

[filed.observe]
type = "exec"
bin  = "github-issue-pr"
args = ["observe"]
`))
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseTaskDocument("task.md", []byte(`+++
[work]
kind              = "task"
resource_observer = "filed"

[work.done_when]
all = [{ check = "resource.state.anything", ne = "" }]
+++
Resolve {{ resource.id }}.
`))
	if err != nil {
		t.Fatal(err)
	}
	v := Validation{From: Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath}}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(context, def)}}, nil)
	if err := v.ValidateTaskContracts(def, registry); err != nil {
		t.Fatalf("an out-of-file contract is unresolvable here, not a rejection: %v", err)
	}
}
