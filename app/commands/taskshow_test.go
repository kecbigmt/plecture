package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestWriteTaskDetail_PrintsNestingChainAsAnOutline(t *testing.T) {
	var buf bytes.Buffer
	err := writeTaskDetail(&buf, &service.TaskDetail{
		ID:         "team_claude",
		Scope:      "run",
		SourcePath: "/cfg/tasks/team_claude.toml",
		Nesting: []service.TaskLayer{
			{ID: "team_claude", Inner: "guarded_claude"},
			{ID: "guarded_claude", Inner: "official/claude/runtime"},
			{ID: "claude"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "Nesting chain (outermost first):\n" +
		"  team_claude (inner = \"guarded_claude\")\n" +
		"    guarded_claude (inner = \"official/claude/runtime\")\n" +
		"      claude\n"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
	}
}

func TestWriteTaskDetail_PlainTaskPrintsNoChainSection(t *testing.T) {
	var buf bytes.Buffer
	if err := writeTaskDetail(&buf, &service.TaskDetail{ID: "claude", Scope: "run"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Nesting chain") {
		t.Errorf("plain task output should have no nesting section; got:\n%s", buf.String())
	}
}

// TestWriteTaskDetail_PrintsExtendsChainWithProvenance is the CLI half of the
// per-element provenance acceptance criterion: every layer names each
// instruction element, its chains, every done_when leaf (not only judges),
// and its schema keys, including which key it gave a default.
func TestWriteTaskDetail_PrintsExtendsChainWithProvenance(t *testing.T) {
	var buf bytes.Buffer
	err := writeTaskDetail(&buf, &service.TaskDetail{
		ID: "work_claude",
		ExtendsChain: []service.TaskExtendsLayer{
			{
				ID:              "work_claude",
				Instructions:    []string{"Use the claude runtime.", "Prefer opus for review."},
				Chains:          []string{"review"},
				StateSchemaKeys: []string{"model (default)"},
			},
			{
				ID:           "work",
				Instructions: []string{"Resolve the issue."},
				DoneWhen:     []string{"check resource.state.checks_status", "judge ac-met"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "Extends chain (outermost first):\n" +
		"  work_claude\n" +
		"    instructions[0]: Use the claude runtime.\n" +
		"    instructions[1]: Prefer opus for review.\n" +
		"    chains: review\n" +
		"    state_schema: model (default)\n" +
		"    work\n" +
		"      instructions[0]: Resolve the issue.\n" +
		"      done_when: check resource.state.checks_status, judge ac-met\n"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
	}
}

func TestFirstLine_MarksATruncatedMultilineInstruction(t *testing.T) {
	if got, want := firstLine("one line only"), "one line only"; got != want {
		t.Errorf("firstLine = %q, want %q", got, want)
	}
	if got, want := firstLine("first\nsecond"), "first …"; got != want {
		t.Errorf("firstLine = %q, want %q", got, want)
	}
}
