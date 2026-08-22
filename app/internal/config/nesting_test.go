package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// loadTasksFromFiles writes each task body under a fresh global-layer
// `tasks/` directory and loads the whole set, so a table row can describe a
// nesting chain as the files an author would actually write.
func loadTasksFromFiles(t *testing.T, files map[string]string) (map[string]TaskDefinition, error) {
	t.Helper()
	base := t.TempDir()
	for id, body := range files {
		writeFile(t, filepath.Join(base, "tasks", id+".toml"), body)
	}
	return (&Config{BaseDir: base}).LoadTaskDefinitions("")
}

// innerRuntime is the plain inner task most rows nest: two outputs, one of
// them mutable, so binding type and mutability rules have something to
// disagree with.
const innerRuntime = `
[claude]
kind  = "effect"
scope = "run"

[claude.setup]
type   = "shell"
script = "true"

[claude.outputs_schema]
type = "object"

[claude.outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }
`

// innerGated is the inner task whose own done_when reads one of its outputs,
// which the outer layer must keep readable by binding it directly.
const innerGated = `
[work]
kind     = "effect"
scope    = "run"
requires = ["review_decision"]

[work.setup]
type   = "shell"
script = "true"

[work.done_when]
all = [ { check = "review_decision", eq = "APPROVED" } ]

[work.outputs_schema]
type = "object"

[work.outputs_schema.properties]
review_decision = { type = "string" }
`

// TestLoadTaskDefinitions_NestedDefinitionParsesEveryLayerField covers the
// whole nested-task field set in one file: the joint vocabulary plus every
// plain-task field an outer layer declares for its own layer.
func TestLoadTaskDefinitions_NestedDefinitionParsesEveryLayerField(t *testing.T) {
	defs, err := loadTasksFromFiles(t, map[string]string{
		"claude": innerRuntime,
		"myclaude": `
[myclaude]
kind     = "effect"
requires = ["pid"]

[myclaude.inner]
uses = "claude"

[myclaude.setup]
type   = "shell"
script = "jq -nc '{guard_dir:\"/tmp/guard\"}'"

[myclaude.cleanup]
type   = "shell"
script = "true"

[myclaude.inner.inputs]
tmux_session = { from = "inputs.tmux_session" }

[myclaude.inner.env]
PLECT_TEAM_CONTEXT = { from = "session.name" }

[myclaude.outputs.bind]
pid = { from = "inner.outputs.pid" }
guard_dir = { from = "locals.guard_dir" }

[myclaude.inputs_schema]
type = "object"

[myclaude.inputs_schema.properties]
tmux_session = { type = "string" }

[myclaude.locals_schema]
type = "object"
required = ["guard_dir"]

[myclaude.locals_schema.properties]
guard_dir = { type = "string" }

[myclaude.outputs_schema]
type = "object"

[myclaude.outputs_schema.properties]
pid       = { type = "integer", mutable = true }
guard_dir = { type = "string" }

[myclaude.done_when]
all = [ { check = "pid", ne = "" }, { judge = "team checklist", id = "team-gate" } ]

[myclaude.done_when.budget]
heartbeats = 20

[myclaude.health.alive]
type   = "shell"
script = "kill -0 \"$pid\""

[myclaude.health.alive.bind]
pid = { from = "self.outputs.pid" }

[[myclaude.chains]]
id       = "review"
workflow = "claude"

[myclaude.chains.when]
all = [ { judge_pending = "team-gate" } ]

[myclaude.chains.inputs]
revision = "{{.Work.outputs.pid}}"
`,
	})
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	def, ok := defs["myclaude"]
	if !ok {
		t.Fatal("task \"myclaude\" was not loaded")
	}
	if def.Inner != "claude" {
		t.Errorf("Inner = %q, want %q", def.Inner, "claude")
	}
	if got := def.InnerInputs["tmux_session"].Source(); got != `{ from = "inputs.tmux_session" }` {
		t.Errorf("inner.inputs[tmux_session] = %s", got)
	}
	if got := def.InnerEnv["PLECT_TEAM_CONTEXT"].Source(); got != `{ from = "session.name" }` {
		t.Errorf("inner.env[PLECT_TEAM_CONTEXT] = %s", got)
	}
	if got := def.OutputsBind["guard_dir"].Source(); got != `{ from = "locals.guard_dir" }` {
		t.Errorf("outputs.bind[guard_dir] = %s", got)
	}
	if _, ok := def.LocalsSchema["properties"]; !ok {
		t.Errorf("locals_schema was not decoded: %v", def.LocalsSchema)
	}
	if def.DoneWhen == nil || len(def.DoneWhen.All) != 2 || def.DoneWhen.Budget == nil {
		t.Errorf("done_when/budget not decoded: %+v", def.DoneWhen)
	}
	if def.Health.AliveProbe() == nil {
		t.Error("[health].alive was not decoded")
	}
	if len(def.Chains) != 1 {
		t.Errorf("chains not decoded: %d chains", len(def.Chains))
	}
	if len(def.InnerChain) != 1 || def.InnerChain[0].ID != "claude" {
		t.Fatalf("InnerChain = %v, want the resolved [claude]", layerIDs(def.InnerChain))
	}
}

func layerIDs(chain []TaskDefinition) []string {
	ids := make([]string, len(chain))
	for i, d := range chain {
		ids[i] = d.ID
	}
	return ids
}

// TestLoadTaskDefinitions_NestedScopeFollowsInnermost covers the effective
// scope rule: an outer layer that declares no scope of its own takes the
// innermost task's, rather than the plain-task "run" default.
func TestLoadTaskDefinitions_NestedScopeFollowsInnermost(t *testing.T) {
	defs, err := loadTasksFromFiles(t, map[string]string{
		"tmux": `
[tmux]
kind = "effect"
scope = "session"

[tmux.setup]
type   = "shell"
script = "true"
`,
		"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "tmux"
`,
	})
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if got := defs["outer"].EffectiveScope(); got != TaskScopeSession {
		t.Errorf("EffectiveScope() = %q, want %q", got, TaskScopeSession)
	}
}

// TestLoadTaskDefinitions_NestingValidationRules is the load-error rule suite
// of docs/design/task-nesting.md's Validation Rules section, one row per rule.
func TestLoadTaskDefinitions_NestingValidationRules(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name: "inner names an unknown task",
			files: map[string]string{
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "nope"
`,
			},
			wantErr: `unknown task "nope"`,
		},
		{
			name: "inner self-reference forms a nesting cycle",
			files: map[string]string{
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "outer"
`,
			},
			wantErr: "nesting cycle",
		},
		{
			name: "inner forms a nesting cycle through another task",
			files: map[string]string{
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "middle"
`,
				"middle": `
[middle]
kind = "effect"

[middle.inner]
uses = "outer"
`,
			},
			wantErr: "nesting cycle",
		},
		{
			name: "the outer task declares a scope that differs from the inner task's",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"
scope = "session"

[outer.inner]
uses = "claude"
`,
			},
			wantErr: "scope",
		},
		{
			name: "an outer produced key collides with another key the same layer produces",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
exit_code = { type = "string" }

[[outer.outputs]]
name   = "exit_code"
script = "echo 0"

[[outer.outputs]]
name   = "exit_code"
script = "echo 1"
`,
			},
			wantErr: "more than once",
		},
		{
			name: "an outer produced key is missing from the outer outputs_schema",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[outer.outputs]]
name   = "exit_code"
script = "echo 0"
`,
			},
			wantErr: "outputs_schema",
		},
		{
			name: "two layers of the nesting chain declare [terminal]",
			files: map[string]string{
				"claude": innerRuntime + `
[claude.terminal.attach]
type   = "shell"
script = "true"
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.terminal.attach]
type   = "shell"
script = "true"

[outer.terminal.capture]
type   = "shell"
script = "true"

[outer.terminal.send_text]
type   = "shell"
script = "true"

[outer.terminal.send_keys]
type   = "shell"
script = "true"
`,
			},
			wantErr: "[terminal]",
		},
		{
			name: "an outer done_when judge id repeats a judge id of another layer",
			files: map[string]string{
				"claude": `
[claude]
kind = "effect"
scope = "run"

[claude.setup]
type   = "shell"
script = "true"

[claude.done_when]
all = [ { judge = "inner criterion", id = "ac-met" } ]

[claude.outputs_schema]
type = "object"

[claude.outputs_schema.properties]
pid = { type = "integer" }
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.done_when]
all = [ { judge = "outer criterion", id = "ac-met" } ]

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer" }
`,
			},
			wantErr: `judge id "ac-met"`,
		},
		{
			name: "an outer done_when check names a key missing from the outer requires",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"
requires = ["pid"]

[outer.inner]
uses = "claude"

[outer.done_when]
all = [ { check = "exit_code", eq = "0" } ]

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid       = { type = "integer", mutable = true }
exit_code = { type = "string" }
`,
			},
			wantErr: "requires",
		},
		{
			name: "the outer requires names a key missing from the outer outputs_schema",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"
requires = ["exit_code"]

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
			wantErr: "outputs schema",
		},
		{
			name: "outputs.bind reads a root the surface does not observe",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "session.name" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "string" }
`,
			},
			wantErr: "effect.outputs.bind",
		},
		{
			name: "outputs.bind declares a public key missing from outputs_schema",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
socket_path = { from = "inner.outputs.socket_path" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
			wantErr: "outputs_schema",
		},
		{
			name: "outputs.bind declares a computed output mutable",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
label = { expr = "'pid-' + string(inner.outputs.pid)" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
label = { type = "string", mutable = true }
`,
			},
			wantErr: "mutable",
		},
		{
			name: "a layer declares [terminal] and the outer contract does not bind interactive_endpoint",
			files: map[string]string{
				"claude": innerRuntime + `
[claude.terminal.attach]
type   = "shell"
script = "true"
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
			wantErr: "interactive_endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadTasksFromFiles(t, tt.files)
			if err == nil {
				t.Fatalf("LoadTaskDefinitions: want an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("LoadTaskDefinitions error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestLoadTaskDefinitions_NestingAcceptsValidChains is the positive half of
// the rule suite: shapes each rule above is meant to leave alone must load.
func TestLoadTaskDefinitions_NestingAcceptsValidChains(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "re-export, rename, and a computed local binding",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.setup]
type   = "shell"
script = "jq -nc '{guard_dir:\"/tmp/guard\"}'"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }
socket = { from = "inner.outputs.socket_path" }
label = { expr = "'pid-' + string(inner.outputs.pid)" }
guard_dir = { from = "locals.guard_dir" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid       = { type = "integer", mutable = true }
socket    = { type = "string" }
label     = { type = "string" }
guard_dir = { type = "string" }
`,
			},
		},
		{
			name: "an inner done_when output kept readable by a direct binding",
			files: map[string]string{
				"work": innerGated,
				"outer": `
[outer]
kind     = "effect"
requires = ["review_decision"]

[outer.inner]
uses = "work"

[outer.done_when]
all = [ { check = "review_decision", eq = "APPROVED" } ]

[outer.outputs.bind]
review_decision = { from = "inner.outputs.review_decision" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
review_decision = { type = "string" }
`,
			},
		},
		{
			name: "an inner done_when output omitted from the outer contract",
			files: map[string]string{
				"work": innerGated,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "work"

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
instruction = { type = "string" }
`,
			},
		},
		{
			name: "an inner done_when output exported through a computed binding",
			files: map[string]string{
				"work": innerGated,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "work"

[outer.outputs.bind]
decision = { expr = "inner.outputs.review_decision + '-checked'" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
decision = { type = "string" }
`,
			},
		},
		{
			name: "an inner layer's chain reading an output the composed contract binds directly",
			files: map[string]string{
				"claude": innerRuntime + `
[[claude.chains]]
id       = "review"
workflow = "claude"

[claude.chains.when]
all = [ { check = "pid", ne = "" } ]

[claude.chains.inputs]
socket = "{{.Work.outputs.socket_path}}"
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }
socket_path = { from = "inner.outputs.socket_path" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }
`,
			},
		},
		{
			name: "an inner layer's chain naming its own key across an outer rename",
			files: map[string]string{
				"work": innerGated + `
[[work.chains]]
id       = "review"
workflow = "claude"

[work.chains.when]
all = [ { check = "review_decision", eq = "APPROVED" } ]

[work.chains.inputs]
decision = "{{.Work.outputs.review_decision}}"
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "work"

[outer.outputs.bind]
decision = { from = "inner.outputs.review_decision" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
decision = { type = "string" }
`,
			},
		},
		{
			name: "an outer chain output outside the composed public contract",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[outer.chains]]
id       = "review"
workflow = "claude"

[outer.chains.when]
all = [ { check = "socket_path", eq = "x" } ]
`,
			},
		},
		{
			name: "an inner chain input omitted from the composed public contract",
			files: map[string]string{
				"claude": innerRuntime + `
[[claude.chains]]
id       = "review"
workflow = "claude"

[claude.chains.when]
all = [ { check = "pid", ne = "" } ]

[claude.chains.inputs]
socket = "{{.Work.outputs.socket_path}}"
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
		},
		{
			name: "a middle layer chain output omitted from the composed public contract",
			files: map[string]string{
				"claude": innerRuntime,
				"middle": `
[middle]
kind = "effect"

[middle.inner]
uses = "claude"

[middle.outputs.bind]
pid = { from = "inner.outputs.pid" }
socket_path = { from = "inner.outputs.socket_path" }

[middle.outputs_schema]
type = "object"

[middle.outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }

[[middle.chains]]
id       = "review"
workflow = "claude"

[middle.chains.when]
all = [ { check = "socket_path", ne = "" } ]
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "middle"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
		},
		{
			name: "an outer layer composing a terminal over a headless inner",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.setup]
type   = "shell"
script = "jq -nc '{pane:\"%1\"}'"

[outer.terminal.attach]
type   = "shell"
script = "true"

[outer.terminal.capture]
type   = "shell"
script = "true"

[outer.terminal.send_text]
type   = "shell"
script = "true"

[outer.terminal.send_keys]
type   = "shell"
script = "true"

[outer.outputs.bind]
interactive_endpoint = { from = "locals.pane" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
interactive_endpoint = { type = "string" }
`,
			},
		},
		{
			name: "three layers with disjoint bind.env keys and a per-layer budget",
			files: map[string]string{
				"claude": innerRuntime,
				"middle": `
[middle]
kind = "effect"

[middle.inner]
uses = "claude"

[middle.inner.env]
PLECT_MIDDLE = "1"

[middle.outputs.bind]
pid = { from = "inner.outputs.pid" }

[middle.outputs_schema]
type = "object"

[middle.outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
				"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "middle"

[outer.inner.env]
PLECT_OUTER = "1"

[outer.outputs.bind]
pid = { from = "inner.outputs.pid" }

[outer.outputs_schema]
type = "object"

[outer.outputs_schema.properties]
pid = { type = "integer", mutable = true }

[outer.done_when]
all = [ { check = "pid", ne = "" } ]

[outer.done_when.budget]
heartbeats = 10
`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadTasksFromFiles(t, tt.files); err != nil {
				t.Fatalf("LoadTaskDefinitions: unexpected error: %v", err)
			}
		})
	}
}

// TestLoadTaskDefinitions_RetiredBindTableRejected: the joint's tables are
// `inner.inputs`, `inner.env`, and `outputs.bind`, so the retired `[bind]`
// spelling fails as the field outside the surface that it is, rather than
// decoding into nothing.
func TestLoadTaskDefinitions_RetiredBindTableRejected(t *testing.T) {
	_, err := loadTasksFromFiles(t, map[string]string{
		"claude": innerRuntime,
		"outer": `
[outer]
kind = "effect"

[outer.inner]
uses = "claude"

[outer.bind.output]
pid = "{{.Inner.outputs.pid}}"
`,
	})
	if err == nil {
		t.Fatal("LoadTaskDefinitions: want an error for an unknown [bind] table, got nil")
	}
	if !strings.Contains(err.Error(), `"bind" is not part of the effect surface`) {
		t.Errorf("LoadTaskDefinitions error = %v, want it to name the unknown bind table", err)
	}
}
