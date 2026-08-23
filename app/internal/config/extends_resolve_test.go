package config

import (
	"path/filepath"
	"testing"
)

// The composed contract is exactly base+deltas: an extension's instruction
// appends after the base's, its own chains join the base's, and done_when
// leaves accumulate — while the base document itself stays exactly what it
// declared, independently referable and untouched by the extension.
func TestValidateTaskDocuments_ComposesInstructionsChainsAndDoneWhen(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind              = "task"
description       = "Implement a fix and hand it to review"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue." }]

[work.done_when]
all = [{ judge = "acceptance criteria are satisfied", id = "ac-met" }]

[work_claude]
kind    = "task"
extends = "work"

[[work_claude.chains]]
id        = "review"
workflow  = "claude_reviewer"
placement = "sibling"

[work_claude.chains.when]
all = [{ judge_pending = "ac-met" }]
`)
	writeFile(t, filepath.Join(base, "workflows", "claude_reviewer.toml"), `
[claude_reviewer]
kind = "workflow"
`)
	cfg := &Config{BaseDir: base}
	docs, observers := loadDocsAndObservers(t, cfg)
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if err := cfg.ValidateTaskDocuments(docs, observers, workflows); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}

	work := docs["work"]
	if work.Instruction != "Resolve the issue." {
		t.Errorf("the base document must stay exactly what it declared: Instruction = %q", work.Instruction)
	}
	if len(work.Chains) != 0 {
		t.Errorf("the base document must not gain the extension's chains: %+v", work.Chains)
	}

	ext, ok := docs["work_claude"]
	if !ok {
		t.Fatalf("work_claude not loaded: %+v", docs)
	}
	if ext.Instruction != "Resolve the issue." {
		t.Errorf("an extension with no instructions of its own inherits the base's: Instruction = %q", ext.Instruction)
	}
	if ext.ResourceObserver != "issue_pr" {
		t.Errorf("ResourceObserver = %q, want it inherited from the base", ext.ResourceObserver)
	}
	if len(ext.Chains) != 1 || ext.Chains[0].ID != "review" {
		t.Fatalf("Chains = %+v, want the extension's own chain composed in", ext.Chains)
	}
	if ext.DoneWhen == nil || len(ext.DoneWhen.All) != 1 || ext.DoneWhen.All[0].ID != "ac-met" {
		t.Fatalf("DoneWhen = %+v, want the base's judge leaf inherited", ext.DoneWhen)
	}
}

// Three layers deep, each layer's instructions append in order and every
// layer's judge id is reachable from the outermost declaration — the
// team-adoption shape unbounded depth exists for.
func TestValidateTaskDocuments_ComposesThreeLayersDeep(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "review.toml"), `
[official_review]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Review the change." }]

[official_review.done_when]
all = [{ judge = "the change is correct", id = "correct" }]

[team_review]
kind         = "task"
extends      = "official_review"
instructions = [{ text = "Additionally check the team style checklist." }]

[team_review.done_when]
all = [{ judge = "the team style checklist is satisfied", id = "team-style" }]

[my_review]
kind         = "task"
extends      = "team_review"
instructions = [{ text = "Additionally leave inline comments." }]
`)
	cfg := &Config{BaseDir: base}
	docs, observers := loadDocsAndObservers(t, cfg)
	if err := cfg.ValidateTaskDocuments(docs, observers, nil); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}
	my := docs["my_review"]
	want := "Review the change.\n\nAdditionally check the team style checklist.\n\nAdditionally leave inline comments."
	if my.Instruction != want {
		t.Errorf("Instruction = %q, want %q", my.Instruction, want)
	}
	if my.DoneWhen == nil || len(my.DoneWhen.All) != 2 {
		t.Fatalf("DoneWhen = %+v, want both ancestors' judges", my.DoneWhen)
	}
	ids := map[string]bool{}
	for _, leaf := range my.DoneWhen.All {
		ids[leaf.ID] = true
	}
	if !ids["correct"] || !ids["team-style"] {
		t.Errorf("judge ids = %v, want both correct and team-style", ids)
	}

	team := docs["team_review"]
	if team.Instruction != "Review the change.\n\nAdditionally check the team style checklist." {
		t.Errorf("the middle layer must compose only up to itself: Instruction = %q", team.Instruction)
	}
}

// TestLoadTaskDocuments_ComposesExtendsWithoutValidateTaskDocuments guards the
// fast display and health-check paths (service.loadDisplayTasks,
// watchdog.EvaluateHealth), which read task documents straight off
// LoadTaskDeclarations/LoadTaskDocuments and deliberately skip the full
// ValidateTaskDocuments contract pass for speed. Composition must not be
// something only the slow path performs, or those callers would evaluate an
// extension's done_when missing every leaf its base contributed.
func TestLoadTaskDocuments_ComposesExtendsWithoutValidateTaskDocuments(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue." }]

[work.done_when]
all = [{ judge = "acceptance criteria are satisfied", id = "ac-met" }]

[work_ext]
kind    = "task"
extends = "work"
`)
	docs, err := (&Config{BaseDir: base}).LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments: %v", err)
	}
	ext, ok := docs["work_ext"]
	if !ok {
		t.Fatalf("work_ext not loaded: %+v", docs)
	}
	if ext.DoneWhen == nil || len(ext.DoneWhen.All) != 1 || ext.DoneWhen.All[0].ID != "ac-met" {
		t.Fatalf("DoneWhen = %+v, want the base's judge leaf composed in without calling ValidateTaskDocuments", ext.DoneWhen)
	}
	if ext.Instruction != "Resolve the issue." {
		t.Errorf("Instruction = %q, want the base's inherited", ext.Instruction)
	}
}

// inputs_schema and state_schema properties merge by key: a new key an
// extension adds joins the base's, and a default the extension adds to an
// existing key survives composition.
func TestValidateTaskDocuments_ComposesSchemaProperties(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind              = "task"
resource_observer = "issue_pr"
instructions      = [{ text = "Resolve the issue." }]

[work.state_schema]
type = "object"

[work.state_schema.properties]
priority = { type = "string" }

[work_ext]
kind    = "task"
extends = "work"

[work_ext.state_schema]
type = "object"

[work_ext.state_schema.properties]
priority    = { type = "string", default = "normal" }
reviewed_by = { type = "string" }
`)
	cfg := &Config{BaseDir: base}
	docs, observers := loadDocsAndObservers(t, cfg)
	if err := cfg.ValidateTaskDocuments(docs, observers, nil); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}
	ext := docs["work_ext"]
	props, ok := ext.StateSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("StateSchema = %+v, want a properties table", ext.StateSchema)
	}
	priority, ok := props["priority"].(map[string]any)
	if !ok || priority["default"] != "normal" {
		t.Errorf("priority = %+v, want the extension's default composed in", priority)
	}
	if _, ok := props["reviewed_by"]; !ok {
		t.Errorf("properties = %+v, want the extension's new key added", props)
	}
}
