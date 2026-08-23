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
