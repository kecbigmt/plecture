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
// per-element provenance acceptance criterion: every layer names the
// instruction, chains, and judges it itself contributes.
func TestWriteTaskDetail_PrintsExtendsChainWithProvenance(t *testing.T) {
	var buf bytes.Buffer
	err := writeTaskDetail(&buf, &service.TaskDetail{
		ID: "work_claude",
		ExtendsChain: []service.TaskExtendsLayer{
			{ID: "work_claude", Instruction: "Use the claude runtime.", Chains: []string{"review"}},
			{ID: "work", Instruction: "Resolve the issue.", Judges: []string{"ac-met"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	want := "Extends chain (outermost first):\n" +
		"  work_claude\n" +
		"    instructions: Use the claude runtime.\n" +
		"    chains: review\n" +
		"    work\n" +
		"      instructions: Resolve the issue.\n" +
		"      judges: ac-met\n"
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
