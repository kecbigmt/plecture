package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/datahome"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
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

func setUpWorkdirWorkflowFixture(t *testing.T) {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	globalDir := filepath.Join(fakeHome, ".config", "plect")
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "resources", "ticket.toml"), `
[ticket]
kind  = "resource"
match = '^ticket://(?P<id>.+)$'
name  = { expr = "match.id" }

[ticket.observe]
type   = "shell"
script = "echo '{}'"

[ticket.state_schema]
type = "object"
`)
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "tasks", "checkout.toml"), `
[checkout]
kind  = "effect"
scope = "session"

[checkout.setup]
type   = "shell"
script = "echo '{}'"

[checkout.health.alive]
type = "noop"

[checkout.outputs_schema]
type = "object"

[checkout.outputs_schema.properties]
dir = { type = "string" }
`)
	writeWorkflowShowFixtureFile(t, filepath.Join(globalDir, "workflows", "inworkdir.toml"), `
[inworkdir]
kind     = "workflow"
resource = "ticket"
workdir  = { from = "nodes.checkout.outputs.dir" }

[[inworkdir.nodes]]
uses = "checkout"
`)
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

func TestWritePopulationMembers_ShowsLastErrorAndStreak(t *testing.T) {
	var buf bytes.Buffer
	err := writePopulationMembers(&buf, []service.PopulationMemberStatus{
		{Resource: "urn:case:a", Session: "a+agent", PendingUp: true,
			LastAdmitReason: "input", LastAdmitError: "resolved to nothing", ConsecutiveAdmitFailures: 3},
		{Resource: "urn:case:b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.HasPrefix(line, "urn:case:a"):
			for _, want := range []string{"a+agent", "true", "input: resolved to nothing", "3"} {
				if !strings.Contains(line, want) {
					t.Errorf("row a missing %q; got:\n%s", want, line)
				}
			}
		case strings.HasPrefix(line, "urn:case:b"):
			for _, want := range []string{"-", "false"} {
				if !strings.Contains(line, want) {
					t.Errorf("row b missing %q; got:\n%s", want, line)
				}
			}
		}
	}
	if !strings.Contains(got, "RESOURCE") {
		t.Errorf("expected a header row; got:\n%s", got)
	}
}

func TestWorkflowPopulationsRetryClearsSuspension(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(datahome.EnvVar, dataDir)
	store := state.NewStore(dataDir)
	if err := store.UpdatePopulation("agent/dispatch", func(population *state.PopulationState) error {
		population.Members[testResource("urn:case:a").Name] = &state.PopulationMember{
			Resource:                 *testResource("urn:case:a"),
			PendingUp:                true,
			ConsecutiveAdmitFailures: 5,
			LastAdmitReason:          "input",
			AdmitSuspended:           true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dataHomeFlag = "" })
	out, err := execRoot(t, "--data-home", dataDir, "workflow", "populations", "retry", "agent", "dispatch", "urn:case:a")
	if err != nil {
		t.Fatalf("workflow populations retry: %v; output:\n%s", err, out)
	}
	population, err := store.Population("agent/dispatch")
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members[testResource("urn:case:a").Name]
	if member.AdmitSuspended || member.ConsecutiveAdmitFailures != 0 || member.LastAdmitReason != "" {
		t.Fatalf("member after CLI retry = %+v, want retry state cleared", member)
	}
}

func TestWorkflowShow_NamesTheEntryResourceAndWorkdir(t *testing.T) {
	setUpWorkdirWorkflowFixture(t)

	out, err := execRoot(t, "workflow", "show", "inworkdir")
	if err != nil {
		t.Fatalf("workflow show: %v; output:\n%s", err, out)
	}
	for _, want := range []string{"Resource:", "ticket", "Workdir:", "nodes.checkout.outputs.dir"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestWorkflowShow_JSONCarriesTheEntryResourceAndWorkdir(t *testing.T) {
	t.Cleanup(func() { workflowShowJSON = false })
	setUpWorkdirWorkflowFixture(t)

	out, err := execRoot(t, "workflow", "show", "--json", "inworkdir")
	if err != nil {
		t.Fatalf("workflow show --json: %v; output:\n%s", err, out)
	}
	var detail service.WorkflowDetail
	if jsonErr := json.Unmarshal([]byte(out), &detail); jsonErr != nil {
		t.Fatalf("--json output is not valid JSON: %v; got:\n%s", jsonErr, out)
	}
	if detail.Resource != "ticket" {
		t.Errorf("resource = %q, want ticket", detail.Resource)
	}
	if !strings.Contains(detail.Workdir, "nodes.checkout.outputs.dir") {
		t.Errorf("workdir = %q, want it to spell the producing node output", detail.Workdir)
	}
	if len(detail.Nodes) != 1 || detail.Nodes[0].ID != "checkout" {
		t.Errorf("nodes = %+v, want the one checkout node", detail.Nodes)
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
