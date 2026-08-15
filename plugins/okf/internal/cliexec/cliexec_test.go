package cliexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withFakePlect puts a fake `plect` script on PATH for the duration of the
// test, so ExistingInstances/SetupPursueGoal can be exercised without a
// real orchestrator or `plect` binary. It records every invocation's
// arguments to a file the test can read back.
func withFakePlect(t *testing.T, script string) (callLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake plect script assumes a POSIX shell")
	}
	dir := t.TempDir()
	callLog = filepath.Join(dir, "calls.log")
	path := filepath.Join(dir, "plect")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\" >> "+callLog+"\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callLog
}

func TestExistingInstances_parsesWorkInstances(t *testing.T) {
	withFakePlect(t, `cat <<'EOF'
{"work":[{"instance":"goal_a"},{"instance":"goal_b"}]}
EOF
`)

	existing, err := CLI{}.ExistingInstances("acme/_orchestrator")
	if err != nil {
		t.Fatalf("ExistingInstances: %v", err)
	}
	if !existing["goal_a"] || !existing["goal_b"] || len(existing) != 2 {
		t.Errorf("existing = %v, want {goal_a, goal_b}", existing)
	}
}

func TestExistingInstances_wrapsCommandFailure(t *testing.T) {
	withFakePlect(t, "echo boom >&2\nexit 1\n")

	_, err := CLI{}.ExistingInstances("acme/_orchestrator")
	if err == nil {
		t.Fatal("want an error when plect status fails")
	}
}

func TestSetupPursueGoal_wrapsCommandFailure(t *testing.T) {
	withFakePlect(t, "echo boom >&2\nexit 1\n")

	err := CLI{}.SetupPursueGoal("acme/_orchestrator", "goal_a", "local-okf://acme/goals/a.md")
	if err == nil {
		t.Fatal("want an error when plect task setup fails")
	}
}

func TestSetupPursueGoal_succeeds(t *testing.T) {
	callLog := withFakePlect(t, "exit 0\n")

	cli := CLI{}
	if err := cli.SetupPursueGoal("acme/_orchestrator", "goal_a", "local-okf://acme/goals/a.md"); err != nil {
		t.Fatalf("SetupPursueGoal: %v", err)
	}

	logged, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(logged); got != "task setup pursue_goal --session acme/_orchestrator --name goal_a --resource local-okf://acme/goals/a.md\n" {
		t.Errorf("unexpected invocation: %q", got)
	}
}
