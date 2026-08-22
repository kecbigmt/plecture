package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupWorkflowFixture(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), `
scope = "run"
setup = "echo '{}'"
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
name        = "Coding agent"
description = "Spawn tmux + agent"

[inputs_schema]
type = "object"

[inputs_schema.properties]
template = { type = "string", description = "Initial prompt name." }

[[nodes]]
uses = "tmux"
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "bare.toml"), `
[[nodes]]
uses = "tmux"
`)
	workdirDir := filepath.Join(tmpHome, "workdirs", "session")
	if err := os.MkdirAll(workdirDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, workdirDir
}

func TestWorkflowList_ReturnsSortedSummaries(t *testing.T) {
	cfg, workdirDir := setupWorkflowFixture(t)
	got, err := WorkflowList(cfg, workdirDir)
	if err != nil {
		t.Fatalf("WorkflowList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 workflows, got %d: %+v", len(got), got)
	}
	if got[0].ID != "bare" || got[1].ID != "coding" {
		t.Errorf("sort order = %q,%q want bare,coding", got[0].ID, got[1].ID)
	}
	if got[1].Name != "Coding agent" {
		t.Errorf("Name = %q", got[1].Name)
	}
	if got[1].Description == "" {
		t.Errorf("Description should be populated")
	}
}

func TestWorkflowShow_ReturnsCompiledDAG(t *testing.T) {
	cfg, workdirDir := setupWorkflowFixture(t)
	got, err := WorkflowShow(cfg, workdirDir, "coding")
	if err != nil {
		t.Fatalf("WorkflowShow: %v", err)
	}
	if got.ID != "coding" || got.Name != "Coding agent" {
		t.Errorf("identity mismatch: %+v", got)
	}
	if len(got.InputsSchema) == 0 {
		t.Errorf("expected inputs_schema to be present")
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "tmux" || got.Nodes[0].Uses != "tmux" {
		t.Errorf("nodes mismatch: %+v", got.Nodes)
	}
}

func TestWorkflowShow_PopulatesChannels(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), "scope = \"run\"\nsetup = \"echo '{}'\"\n")
	writeFile(t, filepath.Join(globalDir, "channels", "claude_channel.toml"), `
[claude_channel]
kind = "channel"
type = "unix_socket"
path = { from = "inputs.path" }
body = { json = { from = "event" } }

[claude_channel.input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "withchan.toml"), `
[[nodes]]
uses = "tmux"

[[event.channel]]
name        = "runtime"
uses        = "claude_channel"
inputs.path = "{{.Nodes.tmux.outputs.session_name}}"
include     = ["plect.instruction", "github.*"]
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := WorkflowShow(cfg, filepath.Join(tmpHome, "workdirs", "session"), "withchan")
	if err != nil {
		t.Fatalf("WorkflowShow: %v", err)
	}
	if len(got.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %+v", got.Channels)
	}
	ch := got.Channels[0]
	if ch.Name != "runtime" || ch.Uses != "claude_channel" || ch.Type != "unix_socket" {
		t.Errorf("channel view = %+v", ch)
	}
	if len(ch.Include) != 2 || ch.Include[0] != "plect.instruction" {
		t.Errorf("channel include = %+v", ch.Include)
	}
}

func TestWorkflowShow_ChannelLoadErrorIsWrapped(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), "scope = \"run\"\nsetup = \"echo '{}'\"\n")
	// unix_socket missing `body` => LoadChannels itself fails (not a validation
	// mismatch), so WorkflowShow returns the wrapped load error, not ErrInvalidInput.
	writeFile(t, filepath.Join(globalDir, "channels", "claude_channel.toml"), "[claude_channel]\nkind = \"channel\"\ntype = \"unix_socket\"\npath = { from = \"inputs.path\" }\n")
	writeFile(t, filepath.Join(globalDir, "workflows", "withchan.toml"), `
[[nodes]]
uses = "tmux"

[[event.channel]]
name        = "runtime"
uses        = "claude_channel"
inputs.path = "p"
include     = ["github.*"]
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = WorkflowShow(cfg, filepath.Join(tmpHome, "workdirs", "session"), "withchan")
	if err == nil {
		t.Fatal("expected error when a channel definition fails to load")
	}
	if _, ok := err.(*Error); ok {
		t.Fatalf("load failure should be a plain wrapped error, got *service.Error: %v", err)
	}
	if !strings.Contains(err.Error(), "load channels") {
		t.Errorf("error = %v, want it to wrap %q", err, "load channels")
	}
}

func TestWorkflowShow_InvalidChannelReturnsInvalidInput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), "scope = \"run\"\nsetup = \"echo '{}'\"\n")
	writeFile(t, filepath.Join(globalDir, "workflows", "withchan.toml"), `
[[nodes]]
uses = "tmux"

[[event.channel]]
name    = "runtime"
uses    = "missing_channel"
include = ["github.*"]
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = WorkflowShow(cfg, filepath.Join(tmpHome, "workdirs", "session"), "withchan")
	if err == nil {
		t.Fatal("expected error for unknown channel definition")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *service.Error, got %T", err)
	}
	if svcErr.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrInvalidInput)
	}
}

func TestWorkflowShow_UnknownIDReturnsInvalidInput(t *testing.T) {
	cfg, workdirDir := setupWorkflowFixture(t)
	_, err := WorkflowShow(cfg, workdirDir, "missing")
	if err == nil {
		t.Fatal("expected error for unknown workflow id")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *service.Error, got %T", err)
	}
	if svcErr.Code != ErrInvalidInput {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrInvalidInput)
	}
}

func TestResolveSessionInputs_RejectsUnknownTask(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeFile(t, filepath.Join(globalDir, "workflows", "claude.toml"), `
[inputs_schema]
type = "object"
required = ["task"]
additionalProperties = false

[inputs_schema.properties.task]
type = "string"
enum = ["work", "review", "respond", "investigate", "none"]

[[nodes]]
uses = "noop"
`)
	cfg, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	_, err := resolveSessionInputs(cfg, "", "claude", map[string]any{"task": "wrok"})
	if err == nil {
		t.Fatal("expected enum validation error")
	}
	const wantChoices = `"task": valid choices are work, review, respond, investigate, none`
	if !strings.Contains(err.Message, wantChoices) {
		t.Errorf("error for invalid task value missing enum choices: %q", err.Message)
	}
	got, err2 := resolveSessionInputs(cfg, "", "claude", map[string]any{"task": "work"})
	if err2 != nil {
		t.Fatalf("valid task rejected: %v", err2)
	}
	if got["task"] != "work" {
		t.Errorf("task = %v, want work", got["task"])
	}
}

// An ad-hoc, gate-less session created without the operator noticing is bad
// enough; an unhelpful error compounds it — a bare "missing
// property" message with no hint at the next command. Omitting a required
// `task` input must surface the enum candidates so an operator/agent knows
// what to pass.
func TestResolveSessionInputs_MissingRequiredTaskListsChoices(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeFile(t, filepath.Join(globalDir, "workflows", "claude.toml"), `
[inputs_schema]
type = "object"
required = ["task"]
additionalProperties = false

[inputs_schema.properties.task]
type = "string"
enum = ["work", "review", "respond", "investigate", "none"]

[[nodes]]
uses = "noop"
`)
	cfg, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	_, err := resolveSessionInputs(cfg, "", "claude", nil)
	if err == nil {
		t.Fatal("expected missing-required validation error")
	}
	if !strings.Contains(err.Message, "missing property") {
		t.Errorf("error lost the underlying schema message: %q", err.Message)
	}
	const wantChoices = `"task": valid choices are work, review, respond, investigate, none`
	if !strings.Contains(err.Message, wantChoices) {
		t.Errorf("error for missing task missing enum choices: %q", err.Message)
	}
}
