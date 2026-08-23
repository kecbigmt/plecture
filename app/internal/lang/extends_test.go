package lang

import "testing"

// resolveTaskDocuments loads every task (and other) definition in src against
// taskContext, validates each definition structurally, then runs
// ValidateTaskContracts against every task definition in declaration order —
// mirroring how the real loader validates every document in a layer, so an
// extends chain spanning more than one definition in src can be exercised.
func resolveTaskDocuments(t *testing.T, src string) error {
	t.Helper()
	context, err := ParseDefinitionDocument("context.toml", []byte(taskContext))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := ParseDefinitionDocument("task.toml", []byte(src))
	if err != nil {
		return err
	}
	for _, def := range defs {
		if def.Kind == KindTask {
			if err := resolveTaskInstructions(def, "."); err != nil {
				return err
			}
		}
	}
	v := Validation{
		From:        Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath},
		Executables: NewExecutableRegistry(PluginExecutables{Alias: fixtureAlias, Path: fixturePath, Names: fixtureExecutables}),
	}
	for _, def := range defs {
		if err := v.ValidateDefinition(def); err != nil {
			return err
		}
	}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(append([]*Definition(nil), context...), defs...)}}, nil)
	for _, def := range defs {
		if def.Kind != KindTask {
			continue
		}
		if err := v.ValidateTaskContracts(def, registry); err != nil {
			return err
		}
	}
	return nil
}

func TestExtendsResolvesStateInheritedFromTheBase(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[base_task.state_schema]
type = "object"

[base_task.state_schema.properties]
verdict_revision = { type = "string" }

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.done_when]
all = [{ check = "self.state.verdict_revision", ne = "" }]
`
	if err := resolveTaskDocuments(t, src); err != nil {
		t.Fatalf("a key the base declares resolves through the chain: %v", err)
	}
}

func TestExtendsAccumulatesJudgeIdsAcrossThreeLayers(t *testing.T) {
	src := `[root_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Root." }]

[root_task.done_when]
all = [{ judge = "the change is correct", id = "correct" }]

[middle_task]
kind    = "task"
extends = "root_task"

[leaf_task]
kind    = "task"
extends = "middle_task"

[leaf_task.done_when]
all = [{ judge = "a different question", id = "correct" }]
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsJudgeIDDuplicate, LayerSemantic)
}

func TestExtendsAllowsAnUnrelatedNewSchemaKey(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[base_task.state_schema]
type = "object"

[base_task.state_schema.properties]
priority = { type = "string" }

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.state_schema]
type = "object"

[ext_task.state_schema.properties]
reviewed_by = { type = "string", default = "" }
`
	if err := resolveTaskDocuments(t, src); err != nil {
		t.Fatalf("a brand new key is always addable: %v", err)
	}
}

func TestExtendsAllowsBudgetAlongsideExtends(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
`
	if err := resolveTaskDocuments(t, src); err != nil {
		t.Fatalf("budget is independent of extends composition: %v", err)
	}
}

func TestExtendsRejectsAComputedReference(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[ext_task]
kind    = "task"
extends = { from = "inputs.base" }
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeRefDynamic, LayerStructural)
}

func TestExtendsRejectsABaseOfAnotherKind(t *testing.T) {
	src := `[ext_task]
kind    = "task"
extends = "goal_reviewer"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeKindMismatch, LayerSemantic)
}

func TestExtendsRequiresTheObserverSomewhereInTheChain(t *testing.T) {
	src := `[root_task]
kind         = "task"
instructions = [{ text = "Root." }]

[leaf_task]
kind    = "task"
extends = "root_task"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeFieldRequired, LayerStructural)
}

func TestExtendsRejectsRequiredOnAnExtensionSchema(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[base_task.state_schema]
type = "object"

[base_task.state_schema.properties]
priority = { type = "string" }

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.state_schema]
type     = "object"
required = ["priority"]
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsSchemaShape, LayerStructural)
}

func TestExtendsRejectsAdditionalPropertiesOnAnExtensionSchema(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.state_schema]
type                 = "object"
additionalProperties = false

[ext_task.state_schema.properties]
reviewed_by = { type = "string" }
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsSchemaShape, LayerStructural)
}

func TestExtendsAllowsTypeAndPropertiesOnAnExtensionSchema(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.state_schema]
type = "object"

[ext_task.state_schema.properties]
reviewed_by = { type = "string" }
`
	if err := resolveTaskDocuments(t, src); err != nil {
		t.Fatalf("type and properties alone are the closed whitelist's whole point: %v", err)
	}
}

func TestExtendsRejectsATypeDisagreement(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[base_task.state_schema]
type = "object"

[base_task.state_schema.properties]
priority = { type = "string" }

[ext_task]
kind    = "task"
extends = "base_task"

[ext_task.state_schema]
type = "array"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsSchemaType, LayerSemantic)
}

func TestExtendsRejectsSchemaFileAcrossMoreThanOneLayer(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]
state_schema_file = "state.schema.json"

[ext_task]
kind    = "task"
extends = "base_task"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsSchemaFileUnsupported, LayerSemantic)
}

func TestExtendsAllowsSchemaFileOnAStandaloneDocument(t *testing.T) {
	src := `[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue." }]
state_schema_file = "state.schema.json"
`
	if err := resolveTaskDocuments(t, src); err != nil {
		t.Fatalf("state_schema_file outside an extends chain stays supported: %v", err)
	}
}

func TestExtendsRejectsADuplicateChainID(t *testing.T) {
	src := `[base_task]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Base." }]

[[base_task.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[base_task.chains.when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]

[base_task.chains.inputs]
task = "review"

[ext_task]
kind    = "task"
extends = "base_task"

[[ext_task.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[ext_task.chains.when]
all = [{ check = "resource.state.checks_status", in = ["FAILURE"] }]

[ext_task.chains.inputs]
task = "review"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsChainIDDuplicate, LayerSemantic)
}

func TestExtendsRejectsAnIndirectCycle(t *testing.T) {
	src := `[task_a]
kind    = "task"
extends = "task_b"

[task_b]
kind    = "task"
extends = "task_c"

[task_c]
kind    = "task"
extends = "task_a"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeExtendsCycle, LayerSemantic)
}
