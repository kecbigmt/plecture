package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeGoal(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validGoal = `---
type: Goal
status: open
---
# Ship it

## Done When

- [ ] write the tests
- [x] design the API
`

func TestParse_success(t *testing.T) {
	dir := t.TempDir()
	path := writeGoal(t, dir, "goal.md", validGoal)

	g, parseErr := Parse(path)
	if parseErr != nil {
		t.Fatalf("unexpected error: %v", parseErr)
	}
	if g.Status != StatusOpen {
		t.Errorf("Status = %q, want open", g.Status)
	}
	if g.ChecklistStatus != ChecklistPending {
		t.Errorf("ChecklistStatus = %q, want PENDING", g.ChecklistStatus)
	}
	if g.OpenItems != "write the tests" {
		t.Errorf("OpenItems = %q, want %q", g.OpenItems, "write the tests")
	}
	if !strings.HasPrefix(g.Revision, "sha256:") {
		t.Errorf("Revision = %q, want sha256: prefix", g.Revision)
	}
}

func TestParse_allChecked(t *testing.T) {
	dir := t.TempDir()
	path := writeGoal(t, dir, "goal.md", strings.ReplaceAll(validGoal, "- [ ] write the tests", "- [x] write the tests"))

	g, parseErr := Parse(path)
	if parseErr != nil {
		t.Fatalf("unexpected error: %v", parseErr)
	}
	if g.ChecklistStatus != ChecklistSuccess {
		t.Errorf("ChecklistStatus = %q, want SUCCESS", g.ChecklistStatus)
	}
	if g.OpenItems != "" {
		t.Errorf("OpenItems = %q, want empty", g.OpenItems)
	}
}

func TestParse_revisionIsStableAndContentDependent(t *testing.T) {
	dir := t.TempDir()
	pathA := writeGoal(t, dir, "a.md", validGoal)
	pathB := writeGoal(t, dir, "b.md", validGoal)
	pathC := writeGoal(t, dir, "c.md", strings.ReplaceAll(validGoal, "write the tests", "write more tests"))

	a, _ := Parse(pathA)
	b, _ := Parse(pathB)
	c, _ := Parse(pathC)

	if a.Revision != b.Revision {
		t.Errorf("identical content produced different revisions: %q vs %q", a.Revision, b.Revision)
	}
	if a.Revision == c.Revision {
		t.Errorf("different content produced the same revision: %q", a.Revision)
	}
}

func TestParse_failureModes(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantStatus string
		wantSubstr string
	}{
		{
			name:       "missing frontmatter",
			content:    "# Just a heading\n",
			wantSubstr: "missing YAML frontmatter",
		},
		{
			name:       "unterminated frontmatter",
			content:    "---\ntype: Goal\nstatus: open\n",
			wantSubstr: "unterminated YAML frontmatter",
		},
		{
			name:       "wrong type",
			content:    "---\ntype: Task\nstatus: open\n---\n## Done When\n- [ ] x\n",
			wantSubstr: `want "Goal"`,
		},
		{
			name:       "invalid status",
			content:    "---\ntype: Goal\nstatus: someday\n---\n## Done When\n- [ ] x\n",
			wantSubstr: "want open|blocked|completed|archived",
		},
		{
			name:       "missing done when section",
			content:    "---\ntype: Goal\nstatus: open\n---\nNo checklist here.\n",
			wantStatus: "open",
			wantSubstr: `missing "## Done When" section`,
		},
		{
			name:       "empty checklist",
			content:    "---\ntype: Goal\nstatus: open\n---\n## Done When\n\nNothing here.\n",
			wantStatus: "open",
			wantSubstr: "no checklist items",
		},
		{
			name:       "malformed checklist item",
			content:    "---\ntype: Goal\nstatus: open\n---\n## Done When\n- [?] what is this\n",
			wantStatus: "open",
			wantSubstr: "malformed checklist item",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeGoal(t, dir, "goal.md", tt.content)

			_, parseErr := Parse(path)
			if parseErr == nil {
				t.Fatal("want a parse error, got none")
			}
			if !strings.Contains(parseErr.Reason, tt.wantSubstr) {
				t.Errorf("Reason = %q, want substring %q", parseErr.Reason, tt.wantSubstr)
			}
			if parseErr.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", parseErr.Status, tt.wantStatus)
			}
			if !strings.HasPrefix(parseErr.Revision, "sha256:") {
				t.Errorf("Revision = %q, want sha256: prefix even on a parse failure", parseErr.Revision)
			}
		})
	}
}

func TestParse_doneWhenSectionStopsAtNextHeading(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntype: Goal\nstatus: open\n---\n## Done When\n- [ ] a\n## Notes\n- [ ] not a checklist item, different section\n"
	path := writeGoal(t, dir, "goal.md", content)

	g, parseErr := Parse(path)
	if parseErr != nil {
		t.Fatalf("unexpected error: %v", parseErr)
	}
	if g.OpenItems != "a" {
		t.Errorf("OpenItems = %q, want only the item under Done When", g.OpenItems)
	}
}

func TestFinalize_writesCompletionAndLog(t *testing.T) {
	dir := t.TempDir()
	path := writeGoal(t, dir, "goal.md", validGoal)
	logPath := filepath.Join(dir, "log.md")
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	if err := Finalize(path, logPath, "local-okf://acme/goals/goal.md", "sha256:abc", now, []Judge{{ID: "goal-met", Reason: "checklist done"}}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "status: completed") {
		t.Errorf("goal file was not marked completed:\n%s", updated)
	}
	if !strings.Contains(string(updated), "completed_at: 2026-08-15T12:00:00Z") {
		t.Errorf("goal file is missing completed_at:\n%s", updated)
	}
	if !strings.Contains(string(updated), "- [ ] write the tests") {
		t.Errorf("Finalize must not touch the checklist body:\n%s", updated)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "goal-met: checklist done") {
		t.Errorf("log entry missing judge evidence:\n%s", logged)
	}
	if !strings.Contains(string(logged), "<!-- local-okf.finalize: local-okf://acme/goals/goal.md @ sha256:abc -->") {
		t.Errorf("log entry missing idempotency marker:\n%s", logged)
	}
}

func TestFinalize_isIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := writeGoal(t, dir, "goal.md", validGoal)
	logPath := filepath.Join(dir, "log.md")
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if err := Finalize(path, logPath, "local-okf://acme/goals/goal.md", "sha256:abc", now, nil); err != nil {
			t.Fatalf("Finalize run %d: %v", i, err)
		}
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logged), "local-okf.finalize:"); got != 1 {
		t.Errorf("finalize marker appeared %d times, want 1:\n%s", got, logged)
	}
}

func TestFinalize_distinctFilesSharingRevisionEachGetLogged(t *testing.T) {
	dir := t.TempDir()
	pathA := writeGoal(t, dir, "a.md", validGoal)
	pathB := writeGoal(t, dir, "b.md", validGoal)
	logPath := filepath.Join(dir, "log.md")
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	if err := Finalize(pathA, logPath, "local-okf://acme/goals/a.md", "sha256:same", now, nil); err != nil {
		t.Fatalf("Finalize a: %v", err)
	}
	if err := Finalize(pathB, logPath, "local-okf://acme/goals/b.md", "sha256:same", now, nil); err != nil {
		t.Fatalf("Finalize b: %v", err)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logged), "local-okf.finalize:"); got != 2 {
		t.Errorf("expected both distinct files to be logged, got %d markers:\n%s", got, logged)
	}
}

func TestFinalize_alreadyCompletedSkipsFrontmatterRewriteButStillLogs(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntype: Goal\nstatus: completed\ncompleted_at: 2020-01-01T00:00:00Z\n---\n## Done When\n- [x] a\n"
	path := writeGoal(t, dir, "goal.md", content)
	logPath := filepath.Join(dir, "log.md")
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	if err := Finalize(path, logPath, "local-okf://acme/goals/goal.md", "sha256:xyz", now, nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "completed_at: 2020-01-01T00:00:00Z") {
		t.Errorf("Finalize must not overwrite an already-recorded completed_at:\n%s", updated)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "local-okf.finalize:") {
		t.Error("an already-completed goal must still get its completion logged")
	}
}

func TestFormatJudges(t *testing.T) {
	if got := FormatJudges(nil); got != "(no judge evidence)" {
		t.Errorf("FormatJudges(nil) = %q", got)
	}
	got := FormatJudges([]Judge{{ID: "a", Reason: "r1"}, {ID: "b", Reason: "r2"}})
	if want := "a: r1; b: r2"; got != want {
		t.Errorf("FormatJudges = %q, want %q", got, want)
	}
}

func TestReadFrontmatter_nonGoalFileIsSkippedNotErrored(t *testing.T) {
	dir := t.TempDir()
	path := writeGoal(t, dir, "index.md", "# Goals\n\nSee the list below.\n")

	fm, ok, err := ReadFrontmatter(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("want ok=false for a file with no frontmatter, got fm=%v", fm)
	}
}

func TestReadFrontmatter_readsAssigneeList(t *testing.T) {
	dir := t.TempDir()
	content := "---\ntype: Goal\nstatus: open\nassignee:\n  - user:alice\n  - team:platform\n---\n## Done When\n- [ ] x\n"
	path := writeGoal(t, dir, "goal.md", content)

	fm, ok, err := ReadFrontmatter(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true")
	}
	assignees, isSlice := fm["assignee"].([]any)
	if !isSlice || len(assignees) != 2 {
		t.Fatalf("assignee = %#v, want a two-element list", fm["assignee"])
	}
}

func TestIsGoalShaped(t *testing.T) {
	dir := t.TempDir()
	goalPath := writeGoal(t, dir, "goal.md", validGoal)
	indexPath := writeGoal(t, dir, "index.md", "# Goals\n")

	if ok, err := IsGoalShaped(goalPath); err != nil || !ok {
		t.Errorf("IsGoalShaped(goal.md) = %v, %v, want true, nil", ok, err)
	}
	if ok, err := IsGoalShaped(indexPath); err != nil || ok {
		t.Errorf("IsGoalShaped(index.md) = %v, %v, want false, nil", ok, err)
	}
}
