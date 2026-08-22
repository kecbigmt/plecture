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
scope = "run"
setup = "true"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }
`

// innerGated is the inner task whose own done_when reads one of its outputs,
// which the outer layer must keep readable by binding it directly.
const innerGated = `
scope = "run"
setup = "true"
requires = ["review_decision"]

[done_when]
all = [ { check = "review_decision", eq = "APPROVED" } ]

[outputs_schema]
type = "object"

[outputs_schema.properties]
review_decision = { type = "string" }
`

// TestLoadTaskDefinitions_NestedDefinitionParsesEveryLayerField covers the
// whole nested-task field set in one file: the joint vocabulary plus every
// plain-task field an outer layer declares for its own layer.
func TestLoadTaskDefinitions_NestedDefinitionParsesEveryLayerField(t *testing.T) {
	defs, err := loadTasksFromFiles(t, map[string]string{
		"claude": innerRuntime,
		"myclaude": `
inner   = "claude"
setup   = "jq -nc '{guard_dir:\"/tmp/guard\"}'"
cleanup = "true"
requires = ["exit_code"]

[bind.inputs]
tmux_session = "{{.Inputs.tmux_session}}"

[bind.env]
PLECT_TEAM_CONTEXT = "{{.SessionName}}"

[bind.outputs]
pid       = "{{.Inner.outputs.pid}}"
guard_dir = "{{.Locals.guard_dir}}"

[inputs_schema]
type = "object"

[inputs_schema.properties]
tmux_session = { type = "string" }

[locals_schema]
type = "object"
required = ["guard_dir"]

[locals_schema.properties]
guard_dir = { type = "string" }

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid       = { type = "integer", mutable = true }
guard_dir = { type = "string" }
exit_code = { type = "string" }

[done_when]
all = [ { check = "exit_code", eq = "0" }, { judge = "team checklist", id = "team-gate" } ]

[done_when.budget]
heartbeats = 20

[health]
alive = "kill -0 {{.Self.pid}}"

[[outputs]]
name   = "exit_code"
script = "echo 0"

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "team-gate" } ]
[chains.inputs]
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
	if got := def.Bind.InputBindings()["tmux_session"]; got != "{{.Inputs.tmux_session}}" {
		t.Errorf("bind.inputs[tmux_session] = %q", got)
	}
	if got := def.Bind.EnvBindings()["PLECT_TEAM_CONTEXT"]; got != "{{.SessionName}}" {
		t.Errorf("bind.env[PLECT_TEAM_CONTEXT] = %q", got)
	}
	if got := def.Bind.OutputBindings()["guard_dir"]; got != "{{.Locals.guard_dir}}" {
		t.Errorf("bind.outputs[guard_dir] = %q", got)
	}
	if _, ok := def.LocalsSchema["properties"]; !ok {
		t.Errorf("locals_schema was not decoded: %v", def.LocalsSchema)
	}
	if def.DoneWhen == nil || len(def.DoneWhen.All) != 2 || def.DoneWhen.Budget == nil {
		t.Errorf("done_when/budget not decoded: %+v", def.DoneWhen)
	}
	if def.Health.AliveProbe() == "" {
		t.Error("[health].alive was not decoded")
	}
	if len(def.DynamicOutputs) != 1 || len(def.Chains) != 1 {
		t.Errorf("outputs/chains not decoded: %d outputs, %d chains", len(def.DynamicOutputs), len(def.Chains))
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
scope = "session"
setup = "true"
`,
		"outer": `
inner = "tmux"
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
				"outer": `inner = "nope"`,
			},
			wantErr: `unknown task "nope"`,
		},
		{
			name: "inner self-reference forms a nesting cycle",
			files: map[string]string{
				"outer": `inner = "outer"`,
			},
			wantErr: "nesting cycle",
		},
		{
			name: "inner forms a nesting cycle through another task",
			files: map[string]string{
				"outer":  `inner = "middle"`,
				"middle": `inner = "outer"`,
			},
			wantErr: "nesting cycle",
		},
		{
			name: "the outer task declares a scope that differs from the inner task's",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
scope = "session"
inner = "claude"
`,
			},
			wantErr: "scope",
		},
		{
			name: "an outer produced key collides with a bind.outputs-bound key",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[outputs]]
name   = "pid"
script = "echo 1"
`,
			},
			wantErr: "collides",
		},
		{
			name: "an outer produced key collides with another key the same layer produces",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[outputs_schema]
type = "object"

[outputs_schema.properties]
exit_code = { type = "string" }

[[outputs]]
name   = "exit_code"
script = "echo 0"

[[outputs]]
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
inner = "claude"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[outputs]]
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
[terminal]
attach    = "true"
capture   = "true"
send_text = "true"
send_keys = "true"
`,
				"outer": `
inner = "claude"

[terminal]
attach    = "true"
capture   = "true"
send_text = "true"
send_keys = "true"
`,
			},
			wantErr: "[terminal]",
		},
		{
			name: "an outer done_when judge id repeats a judge id of another layer",
			files: map[string]string{
				"claude": `
scope = "run"
setup = "true"

[done_when]
all = [ { judge = "inner criterion", id = "ac-met" } ]

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer" }
`,
				"outer": `
inner = "claude"

[done_when]
all = [ { judge = "outer criterion", id = "ac-met" } ]

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
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
inner    = "claude"
requires = ["pid"]

[done_when]
all = [ { check = "exit_code", eq = "0" } ]

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
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
inner    = "claude"
requires = ["exit_code"]

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
			wantErr: "outputs schema",
		},
		{
			name: "bind.outputs references a source other than an inner public output or a local",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "string" }
`,
			},
			wantErr: "neither an inner public output nor a local",
		},
		{
			name: "bind.outputs declares a public key missing from outputs_schema",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
socket_path = "{{.Inner.outputs.socket_path}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
			wantErr: "outputs_schema",
		},
		{
			name: "bind.outputs declares a computed template output mutable",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
label = "pid-{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
label = { type = "string", mutable = true }
`,
			},
			wantErr: "mutable",
		},
		{
			name: "bind.outputs declares a computed template output with a non-string type",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
label = "pid-{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
label = { type = "integer" }
`,
			},
			wantErr: "string",
		},
		{
			name: "a direct binding's outputs_schema type differs from the bound inner output's",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "string" }
`,
			},
			wantErr: "type",
		},
		{
			name: "a direct binding is mutable while the bound inner output is not",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
socket_path = "{{.Inner.outputs.socket_path}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
socket_path = { type = "string", mutable = true }
`,
			},
			wantErr: "mutable",
		},
		{
			name: "bind.inputs omits an inner required input",
			files: map[string]string{
				"claude": `
scope = "run"
setup = "true"

[inputs_schema]
type = "object"
required = ["tmux_session"]
additionalProperties = false

[inputs_schema.properties]
tmux_session = { type = "string" }
model        = { type = "string" }
`,
				"outer": `
inner = "claude"

[bind.inputs]
model = "opus"
`,
			},
			wantErr: `"tmux_session"`,
		},
		{
			name: "bind.inputs binds a key rejected by a closed inner schema",
			files: map[string]string{
				"claude": `
scope = "run"
setup = "true"

[inputs_schema]
type = "object"
additionalProperties = false

[inputs_schema.properties]
model = { type = "string" }
`,
				"outer": `
inner = "claude"

[bind.inputs]
effort = "high"
`,
			},
			wantErr: `"effort"`,
		},
		{
			name: "a bind.env key is not a valid process environment name",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.env]
"PLECT-TEAM" = "x"
`,
			},
			wantErr: "environment",
		},
		{
			name: "a bind.env key repeats a key from another layer of the chain",
			files: map[string]string{
				"claude": innerRuntime,
				"middle": `
inner = "claude"

[bind.env]
PLECT_TEAM_CONTEXT = "middle"
`,
				"outer": `
inner = "middle"

[bind.env]
PLECT_TEAM_CONTEXT = "outer"
`,
			},
			wantErr: "PLECT_TEAM_CONTEXT",
		},
		{
			name: "a chain references a judge id not declared by the composed done_when",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`,
			},
			wantErr: `judge id "ac-met"`,
		},
		{
			name: "a chain reads a local from inside a conditional branch",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"
setup = "jq -nc '{guard_dir:\"/tmp/guard\"}'"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "pid", ne = "" } ]
[chains.inputs]
guard_dir = "{{if .Work.locals.guard_dir}}{{.Work.locals.guard_dir}}{{end}}"
`,
			},
			wantErr: "local",
		},
		{
			name: "a chain references a layer local",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"
setup = "jq -nc '{guard_dir:\"/tmp/guard\"}'"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "pid", eq = "1" } ]
[chains.inputs]
guard_dir = "{{.Work.locals.guard_dir}}"
`,
			},
			wantErr: "local",
		},
		{
			name: "a layer declares [terminal] and the outer contract does not bind interactive_endpoint",
			files: map[string]string{
				"claude": innerRuntime + `
[terminal]
attach    = "true"
capture   = "true"
send_text = "true"
send_keys = "true"
`,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
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
inner = "claude"
setup = "jq -nc '{guard_dir:\"/tmp/guard\"}'"

[bind.outputs]
pid       = "{{.Inner.outputs.pid}}"
socket    = "{{.Inner.outputs.socket_path}}"
label     = "pid-{{.Inner.outputs.pid}}"
guard_dir = "{{.Locals.guard_dir}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
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
inner    = "work"
requires = ["release_ready"]

[done_when]
all = [ { check = "release_ready", eq = "yes" } ]

[bind.outputs]
review_decision = "{{.Inner.outputs.review_decision}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
review_decision = { type = "string" }
release_ready   = { type = "string" }

[[outputs]]
name   = "release_ready"
script = "echo yes"
`,
			},
		},
		{
			name: "an inner done_when output omitted from the outer contract",
			files: map[string]string{
				"work": innerGated,
				"outer": `
inner = "work"

[outputs_schema]
type = "object"

[outputs_schema.properties]
instruction = { type = "string" }
`,
			},
		},
		{
			name: "an inner done_when output exported through a computed binding",
			files: map[string]string{
				"work": innerGated,
				"outer": `
inner = "work"

[bind.outputs]
decision = "{{.Inner.outputs.review_decision}}-checked"

[outputs_schema]
type = "object"

[outputs_schema.properties]
decision = { type = "string" }
`,
			},
		},
		{
			name: "an inner layer's chain reading an output the composed contract binds directly",
			files: map[string]string{
				"claude": innerRuntime + `
[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "pid", ne = "" } ]
[chains.inputs]
socket = "{{.Work.outputs.socket_path}}"
`,
				"outer": `
inner = "claude"

[bind.outputs]
pid         = "{{.Inner.outputs.pid}}"
socket_path = "{{.Inner.outputs.socket_path}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }
`,
			},
		},
		{
			name: "an inner layer's chain naming its own key across an outer rename",
			files: map[string]string{
				"work": innerGated + `
[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "review_decision", eq = "APPROVED" } ]
[chains.inputs]
decision = "{{.Work.outputs.review_decision}}"
`,
				"outer": `
inner = "work"

[bind.outputs]
decision = "{{.Inner.outputs.review_decision}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
decision = { type = "string" }
`,
			},
		},
		{
			name: "an outer chain output outside the composed public contract",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "socket_path", eq = "x" } ]
`,
			},
		},
		{
			name: "an inner chain input omitted from the composed public contract",
			files: map[string]string{
				"claude": innerRuntime + `
[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "pid", ne = "" } ]
[chains.inputs]
socket = "{{.Work.outputs.socket_path}}"
`,
				"outer": `
inner = "claude"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
		},
		{
			name: "a middle layer chain output omitted from the composed public contract",
			files: map[string]string{
				"claude": innerRuntime,
				"middle": `
inner = "claude"

[bind.outputs]
pid         = "{{.Inner.outputs.pid}}"
socket_path = "{{.Inner.outputs.socket_path}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string" }

[[chains]]
id       = "review"
workflow = "claude"
[chains.when]
all = [ { check = "socket_path", ne = "" } ]
`,
				"outer": `
inner = "middle"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
			},
		},
		{
			name: "an outer layer composing a terminal over a headless inner",
			files: map[string]string{
				"claude": innerRuntime,
				"outer": `
inner = "claude"
setup = "jq -nc '{pane:\"%1\"}'"

[terminal]
attach    = "true"
capture   = "true"
send_text = "true"
send_keys = "true"

[bind.outputs]
interactive_endpoint = "{{.Locals.pane}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
interactive_endpoint = { type = "string" }
`,
			},
		},
		{
			name: "three layers with disjoint bind.env keys and a per-layer budget",
			files: map[string]string{
				"claude": innerRuntime,
				"middle": `
inner = "claude"

[bind.env]
PLECT_MIDDLE = "1"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
`,
				"outer": `
inner = "middle"

[bind.env]
PLECT_OUTER = "1"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties]
pid = { type = "integer", mutable = true }

[done_when]
all = [ { check = "pid", ne = "" } ]

[done_when.budget]
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

// TestLoadTaskDefinitions_UnknownBindTableRejected guards the joint's own
// key set the way [terminal] and [health] guard theirs: a misspelled bind
// table must fail loudly instead of decoding into nothing.
func TestLoadTaskDefinitions_UnknownBindTableRejected(t *testing.T) {
	_, err := loadTasksFromFiles(t, map[string]string{
		"claude": innerRuntime,
		"outer": `
inner = "claude"

[bind.output]
pid = "{{.Inner.outputs.pid}}"
`,
	})
	if err == nil {
		t.Fatal("LoadTaskDefinitions: want an error for an unknown [bind] table, got nil")
	}
	if !strings.Contains(err.Error(), `unknown table "output"`) {
		t.Errorf("LoadTaskDefinitions error = %v, want it to name the unknown bind table", err)
	}
}
