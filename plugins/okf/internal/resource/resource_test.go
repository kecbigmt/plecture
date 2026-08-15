package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/okf/internal/goal"
)

const validGoal = `---
type: Goal
status: open
---
## Done When

- [ ] write the tests
`

// fakeRunner answers ResolveOwnerWorkdir with a fixed workdir, or a
// canned failure, without a real orchestrator session.
type fakeRunner struct {
	workdir string
	output  []byte
	err     error
}

func (f fakeRunner) Status(alias string) ([]byte, error) {
	if f.err != nil {
		return f.output, f.err
	}
	return []byte(fmt.Sprintf(`{"runtime":{"workdir_path":%q,"workdir_exists":true}}`, f.workdir)), nil
}

func newBundle(t *testing.T, goalContent string) (workdir string, resourceID string) {
	t.Helper()
	workdir = t.TempDir()
	goalsDir := filepath.Join(workdir, "knowledge", "bundle", "goals")
	if err := os.MkdirAll(goalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalsDir, "ship-it.md"), []byte(goalContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return workdir, "local-okf://acme/goals/ship-it.md"
}

func TestObserve_success(t *testing.T) {
	workdir, resourceID := newBundle(t, validGoal)
	runner := fakeRunner{workdir: workdir}

	result, err := Observe(runner, resourceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GoalParseStatus != ParseStatusSuccess {
		t.Errorf("GoalParseStatus = %q, want SUCCESS", result.GoalParseStatus)
	}
	if result.GoalStatus != goal.StatusOpen {
		t.Errorf("GoalStatus = %q, want open", result.GoalStatus)
	}
	if result.ChecklistStatus != goal.ChecklistPending {
		t.Errorf("ChecklistStatus = %q, want PENDING", result.ChecklistStatus)
	}
	if result.OpenItems != "write the tests" {
		t.Errorf("OpenItems = %q", result.OpenItems)
	}
}

func TestObserve_malformedGoalIsFailureNotError(t *testing.T) {
	workdir, resourceID := newBundle(t, "not frontmatter at all\n")
	runner := fakeRunner{workdir: workdir}

	result, err := Observe(runner, resourceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GoalParseStatus != ParseStatusFailure {
		t.Errorf("GoalParseStatus = %q, want FAILURE", result.GoalParseStatus)
	}
	if result.GoalStatus != "NULL" {
		t.Errorf("GoalStatus = %q, want NULL", result.GoalStatus)
	}
	if result.ObserveError == "" {
		t.Error("ObserveError must explain the failure")
	}
}

func TestObserve_noSessionIsUnresolvedNotError(t *testing.T) {
	runner := fakeRunner{output: []byte("no such session"), err: fmt.Errorf("exit 1")}

	result, err := Observe(runner, "local-okf://acme/goals/ship-it.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GoalParseStatus != ParseStatusUnresolved {
		t.Errorf("GoalParseStatus = %q, want UNRESOLVED", result.GoalParseStatus)
	}
	if result.GoalStatus != "NULL" || result.ChecklistStatus != "NULL" {
		t.Errorf("want NULL/NULL for an unresolved goal, got %q/%q", result.GoalStatus, result.ChecklistStatus)
	}
}

func TestObserve_ambiguousAliasIsAHardError(t *testing.T) {
	runner := fakeRunner{output: []byte("owner:acme matches multiple sessions"), err: fmt.Errorf("exit 1")}

	_, err := Observe(runner, "local-okf://acme/goals/ship-it.md")
	if err == nil {
		t.Fatal("want an error for an ambiguous owner alias")
	}
}

func TestObserve_bundleEscapeIsAHardError(t *testing.T) {
	workdir, _ := newBundle(t, validGoal)
	runner := fakeRunner{workdir: workdir}

	_, err := Observe(runner, "local-okf://acme/../../etc/passwd")
	if err == nil {
		t.Fatal("want an error for a bundle-escaping concept id")
	}
}

func TestFinalize_recordsCompletion(t *testing.T) {
	workdir, resourceID := newBundle(t, validGoal)
	runner := fakeRunner{workdir: workdir}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	if err := Finalize(runner, resourceID, "sha256:abc", now, []goal.Judge{{ID: "goal-met", Reason: "done"}}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	logged, err := os.ReadFile(filepath.Join(workdir, "knowledge", "bundle", "log.md"))
	if err != nil {
		t.Fatalf("expected a log.md to be written: %v", err)
	}
	if !strings.Contains(string(logged), resourceID) {
		t.Errorf("log entry missing resource id:\n%s", logged)
	}
}

func TestFinalize_unresolvedBundleIsAHardError(t *testing.T) {
	runner := fakeRunner{output: []byte("no such session"), err: fmt.Errorf("exit 1")}

	err := Finalize(runner, "local-okf://acme/goals/ship-it.md", "sha256:abc", time.Now(), nil)
	if err == nil {
		t.Fatal("want an error: finalize has no UNRESOLVED state to fold a missing bundle into")
	}
}
