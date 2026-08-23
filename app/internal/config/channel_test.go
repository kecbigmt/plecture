package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

func writeChannelDoc(t *testing.T, baseDir, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(baseDir, "channels", name+".toml"), body)
}

func TestLoadChannels_AcceptsEveryPrimitive(t *testing.T) {
	baseDir := t.TempDir()
	writeChannelDoc(t, baseDir, "runtime", `
[socket]
kind = "channel"
type = "unix_socket"
path = { from = "inputs.path" }
body = { json = { from = "event" } }

[socket.input_schema]
path = { type = "string", required = true }

[process]
kind    = "channel"
type    = "exec"
command = "tmux"
args    = ["send-keys", "-t", { from = "inputs.session" }, "Enter"]
timeout = "5s"

[process.input_schema]
session = { type = "string", required = true }

[imperative]
kind   = "channel"
type   = "shell"
script = "true"

[imperative.bind]
session = { from = "inputs.session" }

[imperative.input_schema]
session = { type = "string", required = true }
`)
	got, err := (&Config{BaseDir: baseDir}).LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels: %v", err)
	}
	socket, ok := got["socket"]
	if !ok || socket.Type != ChannelTypeUnixSocket || socket.Path == nil || socket.Body == nil {
		t.Fatalf("socket channel decoded wrong: %+v", socket)
	}
	if !socket.InputSchema["path"].Required {
		t.Errorf("input_schema not decoded: %+v", socket.InputSchema)
	}
	if socket.Action != nil {
		t.Error("a unix_socket channel runs no process")
	}
	process, ok := got["process"]
	if !ok || process.Type != ChannelTypeExec || process.Action == nil || process.Timeout == nil {
		t.Fatalf("exec channel decoded wrong: %+v", process)
	}
	if len(process.Action.Args) != 4 {
		t.Errorf("exec args = %d, want 4", len(process.Action.Args))
	}
	imperative, ok := got["imperative"]
	if !ok || imperative.Type != ChannelTypeShell || imperative.Action == nil {
		t.Fatalf("shell channel decoded wrong: %+v", imperative)
	}
	if len(imperative.Action.Bind) != 1 {
		t.Errorf("shell bind = %v, want one key", imperative.Action.Bind)
	}
}

func TestLoadChannels_RejectedDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "kind missing",
			body: "[c]\ntype = \"unix_socket\"\n",
			want: "kind",
		},
		{
			name: "unknown primitive",
			body: "[c]\nkind = \"channel\"\ntype = \"webhook\"\n",
			want: "webhook",
		},
		{
			name: "unix_socket missing body",
			body: "[c]\nkind = \"channel\"\ntype = \"unix_socket\"\npath = { from = \"inputs.path\" }\n",
			want: "body",
		},
		{
			name: "unix_socket carrying a process field",
			body: "[c]\nkind = \"channel\"\ntype = \"unix_socket\"\npath = { from = \"inputs.path\" }\nbody = \"b\"\ncommand = \"tmux\"\n",
			want: "command",
		},
		{
			name: "exec naming its executable twice",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"tmux\"\nbin = \"tmux\"\n",
			want: "exactly once",
		},
		{
			name: "exec carrying a unix_socket field",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"tmux\"\npath = { from = \"inputs.path\" }\n",
			want: "path",
		},
		{
			name: "shell script carrying interpolation",
			body: "[c]\nkind = \"channel\"\ntype = \"shell\"\nscript = \"echo {{.Event.body}}\"\n",
			want: "bind",
		},
		{
			name: "a root the delivery surface does not offer",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\nargs = [{ from = \"session.name\" }]\n",
			want: "session.name",
		},
		{
			name: "a timeout reading event data",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\ntimeout = { from = \"event.body\" }\n",
			want: "timeout",
		},
		{
			name: "a literal timeout that is not a duration",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\ntimeout = \"soon\"\n",
			want: "timeout",
		},
		{
			name: "input_schema declaring an unreadable parameter name",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\n\"bad-key\" = { type = \"string\" }\n",
			want: "a parameter name matches",
		},
		{
			name: "input_schema declaring a non-boolean required",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"string\", required = \"true\" }\n",
			want: "\"required\" is a boolean",
		},
		{
			name: "input_schema declaring a non-string default",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"string\", default = 5 }\n",
			want: "\"default\" is a string",
		},
		{
			name: "input_schema declaring a non-string type",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = true }\n",
			want: "\"type\" is a string",
		},
		{
			name: "input_schema declaring a type outside the vocabulary",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"integer\" }\n",
			want: "carries no other type",
		},
		{
			name: "input_schema declaring both required and an empty default",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"string\", required = true, default = \"\" }\n",
			want: "mutually exclusive",
		},
		{
			name: "input_schema missing type",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { required = true }\n",
			want: "`type` is required",
		},
		{
			name: "input_schema declaring both required and default",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"string\", required = true, default = \"x\" }\n",
			want: "mutually exclusive",
		},
		{
			// The whole surface is closed, so a long-retired key like
			// `execution` is caught by this rule too.
			name: "a field outside the channel surface",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\nexecution = \"environment\"\n",
			want: "execution",
		},
		{
			name: "input_schema carrying an unknown field",
			body: "[c]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n\n[c.input_schema]\nsession = { type = \"string\", enum = [\"a\"] }\n",
			want: "enum",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			writeChannelDoc(t, baseDir, "broken", tt.body)
			_, err := (&Config{BaseDir: baseDir}).LoadChannels()
			if err == nil {
				t.Fatalf("expected a load error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not mention %q: %v", tt.want, err)
			}
		})
	}
}

func TestLoadChannels_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	for _, dir := range []string{pluginA, pluginB} {
		writeFile(t, filepath.Join(dir, "config", "channels", "tmux_send_keys.toml"), `
[tmux_send_keys]
kind    = "channel"
type    = "exec"
command = "tmux"
args    = ["send-keys"]
`)
	}
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadChannels()
	if err == nil || !strings.Contains(err.Error(), "tmux_send_keys") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"tmux_send_keys\", got %v", err)
	}
}

func TestValidateWorkflowChannels(t *testing.T) {
	defs := map[string]ChannelDefinition{
		"claude_channel": {Type: ChannelTypeUnixSocket, InputSchema: map[string]ChannelInputSpec{"path": {Type: "string", Required: true}}},
		"slack_thread":   {Type: ChannelTypeExec, InputSchema: map[string]ChannelInputSpec{"channel_id": {Type: "string", Required: true}, "thread_ts": {Type: "string"}}},
	}
	chan1 := func(c ...EventChannel) WorkflowFile { return WorkflowFile{Event: WorkflowEvent{Channel: c}} }
	cases := []struct {
		name    string
		wf      WorkflowFile
		wantErr string
	}{
		{
			name: "ok single",
			wf:   chan1(EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": fromValue("nodes.claude.outputs.socket_path")}, Include: []string{"github.*"}}),
		},
		{
			name: "ok multiple channels",
			wf: chan1(
				EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}, Include: []string{"plect.instruction"}},
				EventChannel{Name: "slack", Uses: "slack_thread", Inputs: map[string]*lang.Value{"channel_id": literalValue("c")}, Include: []string{"github.*", "user.emit"}},
			),
		},
		{
			name:    "unknown uses",
			wf:      chan1(EventChannel{Name: "x", Uses: "webhook", Include: []string{"*"}}),
			wantErr: "unknown channel definition",
		},
		{
			name:    "missing required input",
			wf:      chan1(EventChannel{Name: "runtime", Uses: "claude_channel", Include: []string{"*"}}),
			wantErr: "required input \"path\"",
		},
		{
			name:    "undeclared input",
			wf:      chan1(EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p"), "bogus": literalValue("x")}, Include: []string{"*"}}),
			wantErr: "input \"bogus\" is not declared",
		},
		{
			name: "duplicate name",
			wf: chan1(
				EventChannel{Name: "dup", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}, Include: []string{"*"}},
				EventChannel{Name: "dup", Uses: "slack_thread", Inputs: map[string]*lang.Value{"channel_id": literalValue("c")}, Include: []string{"*"}},
			),
			wantErr: "declared more than once",
		},
		{
			name:    "missing name",
			wf:      chan1(EventChannel{Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}, Include: []string{"*"}}),
			wantErr: "`name` is required",
		},
		{
			name:    "missing uses",
			wf:      chan1(EventChannel{Name: "x", Include: []string{"*"}}),
			wantErr: "`uses` is required",
		},
		{
			name:    "empty include",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}}),
			wantErr: "`include` must list at least one",
		},
		{
			name:    "empty glob",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}, Include: []string{""}}),
			wantErr: "empty glob",
		},
		{
			name:    "metadata selector",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue("p")}, Include: []string{"meta:relay"}}),
			wantErr: "metadata selectors are no longer supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWorkflowChannels(tc.wf, defs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateWorkflowChannels() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateWorkflowChannels() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadWorkflows_ParsesEventChannel(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".config", "plect", "workflows", "coding.toml"), `
[coding]
kind = "workflow"
[[coding.nodes]]
uses = "tmux"

[[coding.event.channel]]
name        = "runtime"
uses        = "claude_channel"
inputs.path = { from = "nodes.claude.outputs.socket_path" }
include     = ["plect.instruction", "github.*"]

[[coding.event.channel]]
name           = "slack"
uses           = "slack_thread"
inputs.channel_id = { from = "nodes.slack_thread.outputs.channel_id" }
include        = ["github.*"]
`)
	cfg := &Config{BaseDir: filepath.Join(repoDir, ".config", "plect")}
	got, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf := got["coding"]
	if len(wf.Event.Channel) != 2 {
		t.Fatalf("expected 2 channels, got %+v", wf.Event.Channel)
	}
	runtime := wf.Event.Channel[0]
	if runtime.Name != "runtime" || runtime.Uses != "claude_channel" {
		t.Errorf("channel[0] = %+v", runtime)
	}
	if runtime.Inputs["path"].From != "nodes.claude.outputs.socket_path" {
		t.Errorf("channel[0] inputs not preserved: %+v", runtime.Inputs)
	}
	if len(runtime.Include) != 2 || runtime.Include[0] != "plect.instruction" {
		t.Errorf("channel[0] include = %+v", runtime.Include)
	}
}

// The ratified cascade appends only `nodes`; every other field a shallower
// layer set is closed, so a deeper layer wanting a different channel set
// declares its own workflow rather than adding to one it cannot see.
func TestLoadWorkflows_CascadeRejectsARedeclaredEventTable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.event.channel]]
name = "runtime"
uses = "claude_channel"
include = ["github.*"]
`)
	// A channel may only come from a trusted layer; use an ancestor above the
	// workdir (not the workdir itself, which the workdir guard forbids).
	orgDir := filepath.Join(tmpHome, "workdirs", "org")
	workdirDir := filepath.Join(orgDir, "repo", "session")
	writeFile(t, filepath.Join(orgDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.event.channel]]
name = "slack"
uses = "slack_thread"
include = ["github.*"]
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.LoadWorkflows(workdirDir)
	if err == nil || !strings.Contains(err.Error(), `field "event" is set by a shallower layer`) {
		t.Fatalf("LoadWorkflows = %v, want a redeclaration error naming `event`", err)
	}
}

func TestLoadWorkflows_RejectsDuplicateChannelNameInFile(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".config", "plect", "workflows", "coding.toml"), `
[coding]
kind = "workflow"
[[coding.event.channel]]
name = "runtime"
uses = "claude_channel"
include = ["github.*"]

[[coding.event.channel]]
name = "runtime"
uses = "tmux_send_keys"
include = ["github.*"]
`)
	cfg := &Config{BaseDir: filepath.Join(repoDir, ".config", "plect")}
	_, err := cfg.LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("LoadWorkflows = %v, want duplicate channel name error", err)
	}
}

// TestShippedConfig_ChannelsValidate guards the real config/plect against typos:
// the workflow↔channel cross-check otherwise runs only on the `show` path, so a
// renamed channel or dropped input would slip through CI.
func TestShippedConfig_ChannelsValidate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..")
	cfgDir := filepath.Join(root, "config", "plect")
	if _, err := os.Stat(cfgDir); err != nil {
		t.Skipf("shipped config not found at %s: %v", cfgDir, err)
	}
	cfg := &Config{BaseDir: cfgDir}
	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped): %v", err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows(shipped): %v", err)
	}
	for _, id := range []string{"claude", "codex"} {
		wf, ok := workflows[id]
		if !ok {
			t.Fatalf("shipped workflow %q not found", id)
		}
		if len(wf.Event.Channel) == 0 {
			t.Errorf("shipped workflow %q declares no event channels", id)
		}
		if err := ValidateWorkflowChannels(wf, channels); err != nil {
			t.Errorf("shipped workflow %q channels invalid: %v", id, err)
		}
	}
}

func TestLoadWorkflows_WorkdirLayerRejectsEventChannel(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workdirDir := filepath.Join(tmpHome, "workdirs", "session")
	writeFile(t, filepath.Join(workdirDir, ".plect", "workflows", "evil.toml"), `
[evil]
kind = "workflow"
[[evil.event.channel]]
name = "runtime"
uses = "tmux_send_keys"
include = ["github.*"]
`)
	cfg := &Config{}
	_, err := cfg.LoadWorkflows(workdirDir)
	if err == nil || !strings.Contains(err.Error(), "may only add [[nodes]]") {
		t.Fatalf("LoadWorkflows = %v, want the workspace-dir node-only error", err)
	}
}

func TestChannelDefinition_ApplyInputDefaults(t *testing.T) {
	def := ChannelDefinition{InputSchema: map[string]ChannelInputSpec{
		"queue_dir":        {Type: "string", Required: true},
		"enqueue_timeout":  {Type: "string", Default: "5s", HasDefault: true},
		"message_envelope": {Type: "string", Default: "[{type}] {body}", HasDefault: true},
		"undeclared":       {Type: "string"},
	}}
	got := def.ApplyInputDefaults(map[string]any{
		"queue_dir":       "/q",
		"enqueue_timeout": "30s",
	})
	want := map[string]any{
		"queue_dir":        "/q",
		"enqueue_timeout":  "30s",
		"message_envelope": "[{type}] {body}",
	}
	if len(got) != len(want) {
		t.Fatalf("inputs = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("input %q = %v, want %v", k, got[k], v)
		}
	}
}

// A declared `default = ""` is a usable default: it is what lets a channel
// pass a flag whose value is legitimately empty, so it must not be folded
// into "no default declared".
func TestChannelDefinition_ApplyInputDefaults_DistinguishesAnEmptyDefault(t *testing.T) {
	baseDir := t.TempDir()
	writeChannelDoc(t, baseDir, "c", `
[c]
kind    = "channel"
type    = "exec"
command = "true"

[c.input_schema]
declared_empty = { type = "string", default = "" }
undeclared     = { type = "string" }
`)
	got, err := (&Config{BaseDir: baseDir}).LoadChannels()
	if err != nil {
		t.Fatal(err)
	}
	inputs := got["c"].ApplyInputDefaults(nil)
	value, set := inputs["declared_empty"]
	if !set {
		t.Error("a declared empty default must be applied")
	}
	if value != "" {
		t.Errorf("declared_empty = %v, want the empty string", value)
	}
	if _, set := inputs["undeclared"]; set {
		t.Error("a parameter with no default must stay unset")
	}
}
