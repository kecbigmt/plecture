package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/service"
)

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
		{Name: "runtime", Uses: "claude_channel", Type: "unix_socket", Include: []string{"sennit.instruction", "github.*"}},
	})
	got := buf.String()
	want := "  runtime (uses claude_channel, unix_socket)\n    delivers: sennit.instruction, github.*\n"
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
