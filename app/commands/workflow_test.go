package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func writeWorkflowShowFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteWorkflowList_NoHeaderUsesLiteralTabs(t *testing.T) {
	var buf bytes.Buffer
	err := writeWorkflowList(&buf, []service.WorkflowSummary{
		{ID: "bare"},
		{ID: "coding-claude", Name: "Coding agent", Description: "desc"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	wantLines := []string{
		"bare\t\t\n",
		"coding-claude\tCoding agent\tdesc\n",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("expected %q in output; got:\n%s", line, got)
		}
	}
	if strings.Contains(got, "ID\tNAME\tDESCRIPTION") {
		t.Errorf("--no-header output should not contain the header row; got:\n%s", got)
	}
}

func TestWriteNodesByScope_GroupsAndPreservesOrder(t *testing.T) {
	var buf bytes.Buffer
	writeNodesByScope(&buf, []service.WorkflowNode{
		{ID: "envfile", Uses: "envfile", Scope: "session"},
		{ID: "slack_thread", Uses: "slack_thread", Scope: "session"},
		{ID: "tmux", Uses: "tmux", Scope: "run"},
		{ID: "claude", Uses: "claude", Scope: "run"},
	})
	got := buf.String()
	want := "  session:\n    envfile\n    slack_thread\n  run:\n    tmux\n    claude\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteNodesByScope_AnnotatesAliasUses(t *testing.T) {
	var buf bytes.Buffer
	writeNodesByScope(&buf, []service.WorkflowNode{
		{ID: "claude_second", Uses: "claude", Scope: "run"},
	})
	got := buf.String()
	if !strings.Contains(got, "claude_second (uses claude)") {
		t.Errorf("expected `uses` annotation for aliased node; got:\n%s", got)
	}
}

func TestWriteChannels_ShowsTypeAndDelivers(t *testing.T) {
	var buf bytes.Buffer
	writeChannels(&buf, []service.WorkflowChannel{
		{Name: "runtime", Uses: "claude_channel", Type: "unix_socket", Include: []string{"plect.instruction", "github.*"}},
	})
	got := buf.String()
	want := "  runtime (uses claude_channel, unix_socket)\n    delivers: plect.instruction, github.*\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteChannels_OmitsBlankTypeAndDelivers(t *testing.T) {
	var buf bytes.Buffer
	writeChannels(&buf, []service.WorkflowChannel{
		{Name: "runtime", Uses: "claude_channel"},
	})
	got := buf.String()
	want := "  runtime (uses claude_channel)\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestWorkflowShow_WorkspaceProviderLoadErrorExitsNonzero(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	globalDir := filepath.Join(fakeHome, ".config", "plect")
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), `
[tmux]
kind  = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo '{}'"
`)
	// `setup` is required for a workspace provider; omitting it fails the load.
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "workspaces", "broken.toml"), `
[broken]
kind = "workspace_provider"
`)
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "workflows", "usesbroken.toml"), `
[usesbroken]
kind = "workflow"
workspace_provider = "broken"

[[usesbroken.nodes]]
uses = "tmux"
`)

	out, err := execRoot(t, "workflow", "show", "usesbroken")
	if err == nil {
		t.Fatalf("expected nonzero exit when the referenced workspace provider fails to load; output:\n%s", out)
	}
	if !strings.Contains(out, "usesbroken") {
		t.Errorf("output should still render the rest of the workflow; got:\n%s", out)
	}
	if !strings.Contains(out, "setup") {
		t.Errorf("output should name the load failure; got:\n%s", out)
	}
}

func TestWriteWorkflowList_HumanFillsBlankSentinels(t *testing.T) {
	var buf bytes.Buffer
	err := writeWorkflowList(&buf, []service.WorkflowSummary{
		{ID: "bare"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "(no name)") {
		t.Errorf("expected (no name) sentinel; got:\n%s", got)
	}
	if !strings.Contains(got, "(no description)") {
		t.Errorf("expected (no description) sentinel; got:\n%s", got)
	}
}
