package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestChannelDefinition_Validate(t *testing.T) {
	cases := []struct {
		name    string
		def     ChannelDefinition
		wantErr string
	}{
		{
			name: "unix_socket ok",
			def:  ChannelDefinition{Type: ChannelTypeUnixSocket, Path: "{{.Inputs.path}}", Body: "{{ json .Event }}"},
		},
		{
			name: "exec ok",
			def:  ChannelDefinition{Type: ChannelTypeExec, Command: "tmux", Args: []string{"send-keys"}},
		},
		{
			name: "unix_socket with timeout ok",
			def:  ChannelDefinition{Type: ChannelTypeUnixSocket, Path: "p", Body: "b", Timeout: Duration{Duration: 5 * time.Second}},
		},
		{
			name:    "unix_socket missing body",
			def:     ChannelDefinition{Type: ChannelTypeUnixSocket, Path: "{{.Inputs.path}}"},
			wantErr: "requires `path` and `body`",
		},
		{
			name:    "unix_socket with exec fields",
			def:     ChannelDefinition{Type: ChannelTypeUnixSocket, Path: "p", Body: "b", Command: "tmux"},
			wantErr: "must not set exec fields",
		},
		{
			name:    "exec missing args",
			def:     ChannelDefinition{Type: ChannelTypeExec, Command: "tmux"},
			wantErr: "requires `command` and at least one `args`",
		},
		{
			name:    "exec with unix_socket fields",
			def:     ChannelDefinition{Type: ChannelTypeExec, Command: "tmux", Args: []string{"x"}, Path: "p"},
			wantErr: "must not set unix_socket fields",
		},
		{
			name:    "empty type",
			def:     ChannelDefinition{},
			wantErr: "`type` is required",
		},
		{
			name:    "unknown type",
			def:     ChannelDefinition{Type: "webhook"},
			wantErr: "unknown channel type",
		},
		{
			name:    "input_schema missing type",
			def:     ChannelDefinition{Type: ChannelTypeExec, Command: "c", Args: []string{"a"}, InputSchema: map[string]ChannelInputSpec{"session": {Required: true}}},
			wantErr: "`type` is required",
		},
		{
			name: "exec with execution=environment ok",
			def:  ChannelDefinition{Type: ChannelTypeExec, Command: "tmux", Args: []string{"send-keys"}, Execution: ExecutionEnvironment},
		},
		{
			name: "exec with execution=host ok",
			def:  ChannelDefinition{Type: ChannelTypeExec, Command: "tmux", Args: []string{"send-keys"}, Execution: ExecutionHost},
		},
		{
			name:    "exec with invalid execution",
			def:     ChannelDefinition{Type: ChannelTypeExec, Command: "tmux", Args: []string{"send-keys"}, Execution: "docker"},
			wantErr: "`execution` must be",
		},
		{
			name:    "unix_socket must not set execution",
			def:     ChannelDefinition{Type: ChannelTypeUnixSocket, Path: "p", Body: "b", Execution: ExecutionHost},
			wantErr: "must not set `execution`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadChannels(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "channels", "claude_channel.toml"), `
type = "unix_socket"
path = "{{.Inputs.path}}"
body = "{{ json .Event }}"

[input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(globalDir, "channels", "tmux_send_keys.toml"), `
type = "exec"
command = "tmux"
args = ["send-keys", "-t", "{{.Inputs.session}}", "Enter"]
timeout = "5s"

[input_schema]
session = { type = "string", required = true }
`)
	cfg := Load()
	got, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels: %v", err)
	}
	cc, ok := got["claude_channel"]
	if !ok {
		t.Fatalf("claude_channel missing from %+v", got)
	}
	if cc.Type != ChannelTypeUnixSocket || cc.Body != "{{ json .Event }}" {
		t.Errorf("claude_channel decoded wrong: %+v", cc)
	}
	if !cc.InputSchema["path"].Required {
		t.Errorf("claude_channel input_schema not decoded: %+v", cc.InputSchema)
	}
	tk, ok := got["tmux_send_keys"]
	if !ok {
		t.Fatalf("tmux_send_keys missing from %+v", got)
	}
	if tk.Type != ChannelTypeExec || tk.Timeout.Duration.String() != "5s" || len(tk.Args) != 4 {
		t.Errorf("tmux_send_keys decoded wrong: %+v", tk)
	}
}

func TestLoadChannels_RejectsInvalid(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "channels", "broken.toml"), `
type = "unix_socket"
path = "{{.Inputs.path}}"
`)
	cfg := Load()
	if _, err := cfg.LoadChannels(); err == nil {
		t.Fatal("expected error for unix_socket channel missing body")
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
			wf:   chan1(EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]string{"path": "{{.Nodes.claude.outputs.socket_path}}"}, Include: []string{"github.*"}}),
		},
		{
			name: "ok multiple channels",
			wf: chan1(
				EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]string{"path": "p"}, Include: []string{"plect.instruction"}},
				EventChannel{Name: "slack", Uses: "slack_thread", Inputs: map[string]string{"channel_id": "c"}, Include: []string{"github.*", "user.emit"}},
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
			wf:      chan1(EventChannel{Name: "runtime", Uses: "claude_channel", Inputs: map[string]string{"path": "p", "bogus": "x"}, Include: []string{"*"}}),
			wantErr: "input \"bogus\" is not declared",
		},
		{
			name: "duplicate name",
			wf: chan1(
				EventChannel{Name: "dup", Uses: "claude_channel", Inputs: map[string]string{"path": "p"}, Include: []string{"*"}},
				EventChannel{Name: "dup", Uses: "slack_thread", Inputs: map[string]string{"channel_id": "c"}, Include: []string{"*"}},
			),
			wantErr: "declared more than once",
		},
		{
			name:    "missing name",
			wf:      chan1(EventChannel{Uses: "claude_channel", Inputs: map[string]string{"path": "p"}, Include: []string{"*"}}),
			wantErr: "`name` is required",
		},
		{
			name:    "missing uses",
			wf:      chan1(EventChannel{Name: "x", Include: []string{"*"}}),
			wantErr: "`uses` is required",
		},
		{
			name:    "empty include",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]string{"path": "p"}}),
			wantErr: "`include` must list at least one",
		},
		{
			name:    "empty glob",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]string{"path": "p"}, Include: []string{""}}),
			wantErr: "empty glob",
		},
		{
			name:    "metadata selector",
			wf:      chan1(EventChannel{Name: "x", Uses: "claude_channel", Inputs: map[string]string{"path": "p"}, Include: []string{"meta:relay"}}),
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
[[nodes]]
uses = "tmux"

[[event.channel]]
name        = "runtime"
uses        = "claude_channel"
inputs.path = "{{.Nodes.claude.outputs.socket_path}}"
include     = ["plect.instruction", "github.*"]

[[event.channel]]
name           = "slack"
uses           = "slack_thread"
inputs.channel_id = "{{.Nodes.slack_thread.outputs.channel_id}}"
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
	if runtime.Inputs["path"] != "{{.Nodes.claude.outputs.socket_path}}" {
		t.Errorf("channel[0] inputs not preserved: %+v", runtime.Inputs)
	}
	if len(runtime.Include) != 2 || runtime.Include[0] != "plect.instruction" {
		t.Errorf("channel[0] include = %+v", runtime.Include)
	}
}

func TestLoadWorkflows_CascadeAppendsChannels(t *testing.T) {
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
[[event.channel]]
name = "runtime"
uses = "claude_channel"
include = ["github.*"]
`)
	// A channel may only come from a trusted layer; use an ancestor above the
	// workdir (not the workdir itself, which the workdir guard forbids).
	orgDir := filepath.Join(tmpHome, "workdirs", "org")
	workdirDir := filepath.Join(orgDir, "repo", "session")
	writeFile(t, filepath.Join(orgDir, ".plect", "workflows", "shared.toml"), `
[[event.channel]]
name = "slack"
uses = "slack_thread"
include = ["github.*"]
`)
	cfg := Load()
	got, err := cfg.LoadWorkflows(workdirDir)
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf := got["shared"]
	if len(wf.Event.Channel) != 2 {
		t.Fatalf("expected 2 merged channels, got %+v", wf.Event.Channel)
	}
	if wf.Event.Channel[0].Name != "runtime" || wf.Event.Channel[1].Name != "slack" {
		t.Errorf("merge order wrong: %+v", wf.Event.Channel)
	}
}

func TestLoadWorkflows_CascadeRejectsDuplicateChannelName(t *testing.T) {
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
[[event.channel]]
name = "runtime"
uses = "claude_channel"
include = ["github.*"]
`)
	// Redeclare from a trusted ancestor so the dup-name check fires, not the
	// workdir guard (which would reject any event.channel in the workdir).
	orgDir := filepath.Join(tmpHome, "workdirs", "org")
	workdirDir := filepath.Join(orgDir, "repo", "session")
	writeFile(t, filepath.Join(orgDir, ".plect", "workflows", "shared.toml"), `
[[event.channel]]
name = "runtime"
uses = "tmux_send_keys"
include = ["github.*"]
`)
	cfg := Load()
	_, err := cfg.LoadWorkflows(workdirDir)
	if err == nil || !strings.Contains(err.Error(), "declared in both") {
		t.Fatalf("LoadWorkflows = %v, want duplicate channel name error", err)
	}
}

func TestLoadWorkflows_RejectsDuplicateChannelNameInFile(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".config", "plect", "workflows", "coding.toml"), `
[[event.channel]]
name = "runtime"
uses = "claude_channel"
include = ["github.*"]

[[event.channel]]
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
[[event.channel]]
name = "runtime"
uses = "tmux_send_keys"
include = ["github.*"]
`)
	cfg := &Config{}
	_, err := cfg.LoadWorkflows(workdirDir)
	if err == nil || !strings.Contains(err.Error(), "event.channel") {
		t.Fatalf("LoadWorkflows = %v, want error mentioning event.channel", err)
	}
}
