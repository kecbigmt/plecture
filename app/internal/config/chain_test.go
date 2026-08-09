package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTaskFile writes a single task definition file directly (not via the
// taskFixture helper, which lives in the service package): the config
// package's own tests write `tasks/<id>.toml` under a base dir.
func writeTaskFile(t *testing.T, base, id, body string) {
	t.Helper()
	dir := filepath.Join(base, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AC5 (dual-read retired): a `chains/*.toml` file sitting under the config
// base dir is no longer read at all — not merged, not an error. Only
// task-embedded `[[chains]]` are loaded. LegacyChainsDirNotice separately
// surfaces its presence as a migration-nudge warning (see
// TestLegacyChainsDirNotice_WarnsPerFile below) — TaskChains itself stays
// silent and simply excludes it.
func TestChains_LegacyDirIsIgnored(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "chains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]
`)

	defs, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if got := TaskChains(defs); len(got) != 0 {
		t.Fatalf("expected the legacy chains/*.toml declaration to be ignored, got %+v", got)
	}
}

// A surviving chains/*.toml file gets one warning naming it, so a migration
// straggler has a signal that the rule stopped firing instead of silence.
func TestLegacyChainsDirNotice_WarnsPerFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "chains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte("[[chains]]\nid=\"review\"\nworkflow=\"codex\"\n[chains.when]\nall=[{judge_pending=\"x\"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnings, err := (&Config{BaseDir: base}).LegacyChainsDirNotice()
	if err != nil {
		t.Fatalf("LegacyChainsDirNotice: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "review.toml") {
		t.Fatalf("expected one warning naming the file, got %v", warnings)
	}
}

// No chains/ dir at all is the common case and must not warn.
func TestLegacyChainsDirNotice_NoDirIsSilent(t *testing.T) {
	warnings, err := (&Config{BaseDir: t.TempDir()}).LegacyChainsDirNotice()
	if err != nil {
		t.Fatalf("LegacyChainsDirNotice: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestChainDefinition_Validate(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing workflow",
			body:    "[[chains]]\nid=\"r\"\n[chains.when]\nall=[{judge_pending=\"x\"}]\n",
			wantErr: "`workflow` is required",
		},
		{
			name:    "empty when",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[]\n",
			wantErr: "declares no facts",
		},
		{
			name:    "bad placement",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\nplacement=\"cousin\"\n[chains.when]\nall=[{judge_pending=\"x\"}]\n",
			wantErr: "is not",
		},
		{
			name:    "check with two operators",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[{check=\"s\", eq=\"A\", ne=\"B\"}]\n",
			wantErr: "exactly one operator",
		},
		{
			name:    "two fact kinds",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[{check=\"s\", in=[\"A\"], judge_pending=\"x\"}]\n",
			wantErr: "more than one",
		},
		{
			name:    "judge_action without is",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[{judge_action=\"x\"}]\n",
			wantErr: "`is` to be",
		},
		{
			name:    "judge_action bad is",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[{judge_action=\"x\", is=\"changes_requested\"}]\n",
			wantErr: "`is` to be",
		},
		{
			name:    "bad id",
			body:    "[[chains]]\nid=\"a/b\"\nworkflow=\"codex\"\n[chains.when]\nall=[{judge_pending=\"x\"}]\n",
			wantErr: "characters outside",
		},
		{
			name:    "malformed input template",
			body:    "[[chains]]\nid=\"r\"\nworkflow=\"codex\"\n[chains.when]\nall=[{judge_pending=\"x\"}]\n[chains.inputs]\nrevision=\"{{.Work.outputs.revision\"\n",
			wantErr: "input \"revision\"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeTaskFile(t, base, "work", "scope = \"run\"\nsetup = \"true\"\n\n"+tc.body)
			_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

// AC (parse): a task-embedded [[chains]] loads with TaskID/SourcePath stamped.
func TestLoadTaskDefinitions_ParsesEmbeddedChains(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id        = "review"
workflow  = "claude"
placement = "sibling"

[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)
	defs, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	def, ok := defs["work"]
	if !ok {
		t.Fatalf("task %q not loaded", "work")
	}
	if len(def.Chains) != 1 {
		t.Fatalf("Chains = %+v, want 1 entry", def.Chains)
	}
	ch := def.Chains[0]
	if ch.ID != "review" || ch.Workflow != "claude" {
		t.Fatalf("chain = %+v", ch)
	}
	if ch.TaskID != "work" {
		t.Errorf("TaskID = %q, want %q", ch.TaskID, "work")
	}
	if ch.SourcePath != def.SourcePath {
		t.Errorf("SourcePath = %q, want %q", ch.SourcePath, def.SourcePath)
	}
}

// AC2: a chain's `when` referencing a judge id absent from this task's
// done_when is a load error, not deferred to fire time.
func TestLoadTaskDefinitions_ChainUnknownJudgeID(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "nope" } ]
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected unknown-judge-id error naming %q, got %v", "nope", err)
	}
}

// judge_action is the other judge-referencing fact kind and must be checked
// the same way as judge_pending.
func TestLoadTaskDefinitions_ChainUnknownJudgeActionID(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_action = "nope", is = "approve" } ]
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown-judge-id error naming %q, got %v", "nope", err)
	}
}

// A task with no done_when at all declares an empty judge-id set, so any
// judge-referencing chain on it is necessarily an error.
func TestLoadTaskDefinitions_ChainJudgeReferenceWithoutDoneWhen(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "ac-met") {
		t.Fatalf("expected error naming %q, got %v", "ac-met", err)
	}
}

// AC3: a [chains.inputs] binding wiring an output key absent from this task's
// outputs_schema is a load error.
func TestLoadTaskDefinitions_ChainWiresUndeclaredOutput(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[outputs_schema]
type = "object"
[outputs_schema.properties.checks_status]
type = "string"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "revision") || !strings.Contains(err.Error(), "outputs_schema") {
		t.Fatalf("expected undeclared-output error naming %q, got %v", "revision", err)
	}
}

// A task declaring no outputs_schema has no wiring contract to violate, so
// the wiring check is skipped entirely rather than rejecting every binding.
func TestLoadTaskDefinitions_ChainWiringSkipsWithoutSchema(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`)
	defs, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if len(defs["work"].Chains) != 1 {
		t.Fatalf("expected the chain to load, got %+v", defs["work"].Chains)
	}
}

// A chain id declared twice within the same task file is a load error (the
// task's own chains must be uniquely identified for spawn-tag purposes).
func TestLoadTaskDefinitions_ChainDuplicateIDWithinTask(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("expected duplicate-chain-id error, got %v", err)
	}
}

// A structurally invalid embedded chain (ChainDefinition.Validate failure)
// surfaces the same as any other load error.
func TestLoadTaskDefinitions_ChainValidateFailure(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "work", `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id = "review"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "`workflow` is required") {
		t.Fatalf("expected Validate() failure to surface, got %v", err)
	}
}

func chainDef(id, taskID, sourcePath string) ChainDefinition {
	return ChainDefinition{
		ID:         id,
		Workflow:   "claude",
		TaskID:     taskID,
		SourcePath: sourcePath,
		When:       ChainWhen{All: []ChainWhenFact{{JudgePending: "ac-met"}}},
	}
}

// The same chain id may be declared by two different tasks — each is scoped
// to its own task's instances, so there is no ambiguity to reject.
func TestTaskChains_CrossTaskDuplicateIDsAllowed(t *testing.T) {
	defs := map[string]TaskDefinition{
		"a": {ID: "a", Chains: []ChainDefinition{chainDef("review", "a", "tasks/a.toml")}},
		"b": {ID: "b", Chains: []ChainDefinition{chainDef("review", "b", "tasks/b.toml")}},
	}
	out := TaskChains(defs)
	if len(out) != 2 {
		t.Fatalf("expected both task-scoped chains to survive, got %+v", out)
	}
}
