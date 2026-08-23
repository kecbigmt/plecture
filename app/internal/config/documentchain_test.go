package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A chain's structural invariants are checked at load, before anything that
// needs the rest of the layer.
func TestDocumentChain_Validate(t *testing.T) {
	tests := []struct {
		name    string
		chain   string
		wantErr string
	}{
		{
			name:    "missing workflow",
			chain:   "[[pursue.chains]]\nid=\"r\"\n[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n",
			wantErr: "`workflow` is required",
		},
		{
			name:    "empty when",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[]\n",
			wantErr: "declares no facts",
		},
		{
			name:    "bad placement",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\nplacement=\"cousin\"\n[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n",
			wantErr: "is not",
		},
		{
			name:    "check with two operators",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{check=\"resource.state.revision\", eq=\"A\", ne=\"B\"}]\n",
			wantErr: "exactly one operator",
		},
		{
			name:    "two fact kinds",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{check=\"resource.state.revision\", in=[\"A\"], judge_pending=\"goal-met\"}]\n",
			wantErr: "more than one",
		},
		{
			name:    "judge_action without is",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{judge_action=\"goal-met\"}]\n",
			wantErr: "`is` to be",
		},
		{
			name:    "judge_action bad is",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{judge_action=\"goal-met\", is=\"changes_requested\"}]\n",
			wantErr: "`is` to be",
		},
		{
			name:    "input reaches a root this surface does not offer",
			chain:   "[[pursue.chains]]\nid=\"r\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n[pursue.chains.inputs]\nsecret = { from = \"locals.guard_dir\" }\n",
			wantErr: "is not a root",
		},
		{
			name:    "bad id",
			chain:   "[[pursue.chains]]\nid=\"a/b\"\nworkflow=\"wf\"\n[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n",
			wantErr: "characters outside",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
			writeFile(t, filepath.Join(base, "tasks", "pursue.toml"), `
[pursue]
kind              = "task"
description       = "Pursue one goal"
resource_observer = "issue_pr"
instructions      = [{ text = "Pursue the goal." }]

[pursue.done_when]
all = [{ judge = "the goal is achieved", id = "goal-met" }]

`+tc.chain)
			_, err := (&Config{BaseDir: base}).LoadTaskDocuments("")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

// A chain's `when` names a judge leaf its own document declares, and its
// inputs read keys one of the two contracts declares. Both resolve against
// the rest of the layer, so they are ValidateTaskDocuments' rules.
func TestValidateTaskDocuments_ChainReferences(t *testing.T) {
	tests := []struct {
		name string
		// workflow overrides the chain's target, defaulting to the one the
		// fixture layer declares.
		workflow string
		chain    string
		wantErr  string
	}{
		{
			name:    "unknown judge id",
			chain:   "[pursue.chains.when]\nall=[{judge_pending=\"nope\"}]\n",
			wantErr: "names no judge leaf",
		},
		{
			name:    "input reads an undeclared key",
			chain:   "[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n[pursue.chains.inputs]\nrev = { from = \"resource.state.nope\" }\n",
			wantErr: "names no property",
		},
		{
			// The target is resolved against the parsed workflow
			// declarations, so a chain naming one nothing declares is a load
			// error rather than a fire-time surprise.
			name:     "unknown target workflow",
			workflow: "nope",
			chain:    "[pursue.chains.when]\nall=[{judge_pending=\"goal-met\"}]\n",
			wantErr:  "PLECTURE-CFG-UNKNOWN-REF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
			writeFile(t, filepath.Join(base, "workflows", "wf.toml"), "[wf]\nkind = \"workflow\"\n\n[[wf.nodes]]\nuses = \"noop\"\n")
			target := tc.workflow
			if target == "" {
				target = "wf"
			}
			writeFile(t, filepath.Join(base, "tasks", "pursue.toml"), `
[pursue]
kind              = "task"
description       = "Pursue one goal"
resource_observer = "issue_pr"
instructions      = [{ text = "Pursue the goal." }]

[pursue.done_when]
all = [{ judge = "the goal is achieved", id = "goal-met" }]

[[pursue.chains]]
id       = "review"
workflow = "`+target+`"

`+tc.chain)
			cfg := &Config{BaseDir: base}
			docs, observers := loadDocsAndObservers(t, cfg)
			workflows, err := cfg.LoadWorkflows("")
			if err != nil {
				t.Fatalf("LoadWorkflows: %v", err)
			}
			err = cfg.ValidateTaskDocuments(docs, observers, workflows)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}
