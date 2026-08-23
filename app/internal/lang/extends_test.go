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

// TestExtendsResolvesStateInheritedFromTheBase is the regression this feature
// exists to fix: an extension's own done_when reads a self.state key its base
// declares and it never restates, which a per-document contract check would
// otherwise reject as unpublished.
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

// TestExtendsAccumulatesJudgeIdsAcrossThreeLayers proves the duplicate check
// walks the whole chain, not just the immediate base: a grandchild reusing
// the root's judge id collides even though its own parent declares none.
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

// TestExtendsAllowsAnUnrelatedNewSchemaKey confirms the closed whitelist's
// "new keys freely addable" half: adding a key the base never declared, with
// its own default, is not a redeclaration of anything.
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

// TestExtendsAllowsBudgetAlongsideExtends documents that budget, unlike
// resource_observer, is not part of the closed whitelist but also is not
// forbidden: each declaration's own convergence bound is independent of
// composition, so an extension may set its own.
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

// TestExtendsRejectsAComputedReference holds extends to the same static
// topology rule as workspace_provider and inner.uses.
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

// TestExtendsRejectsABaseOfAnotherKind holds extends to the same expected-kind
// rule every other reference site follows.
func TestExtendsRejectsABaseOfAnotherKind(t *testing.T) {
	src := `[ext_task]
kind    = "task"
extends = "goal_reviewer"
`
	wantDiag(t, resolveTaskDocuments(t, src), CodeKindMismatch, LayerSemantic)
}

// TestExtendsRequiresTheObserverSomewhereInTheChain proves the required-field
// rule still fires when the missing observer is several layers away from the
// document under validation.
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

// TestExtendsRejectsAnIndirectCycle proves cycle detection walks the whole
// chain, not just an immediate self-reference: three declarations extending
// each other in a loop reach themselves just as surely as one.
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
