package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withFakePlect puts a fake `plect status ... --json --full` on PATH,
// answering with a fixed workspace directory, so the CLI's `run` can be
// exercised end-to-end without a real orchestrator session.
func withFakePlect(t *testing.T, workspaceDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake plect script assumes a POSIX shell")
	}
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat <<EOF\n{\"runtime\":{\"workspace_dir_path\":%q,\"workspace_dir_exists\":true}}\nEOF\n", workspaceDir)
	path := filepath.Join(dir, "plect")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newBundle(t *testing.T, goalContent string) (workspaceDir string) {
	t.Helper()
	workspaceDir = t.TempDir()
	goalsDir := filepath.Join(workspaceDir, "knowledge", "bundle", "goals")
	if err := os.MkdirAll(goalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalsDir, "ship-it.md"), []byte(goalContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return workspaceDir
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), runErr
}

const validGoal = "---\ntype: Goal\nstatus: open\n---\n## Done When\n\n- [ ] write the tests\n"

func TestRun_resourceObserve(t *testing.T) {
	workspaceDir := newBundle(t, validGoal)
	withFakePlect(t, workspaceDir)

	stdout, err := captureStdout(t, func() error {
		return run([]string{"resource", "observe", "--resource", "local-okf://acme/goals/ship-it.md"})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if got["goal_parse_status"] != "SUCCESS" {
		t.Errorf("goal_parse_status = %v, want SUCCESS", got["goal_parse_status"])
	}
	if got["goal_status"] != "open" {
		t.Errorf("goal_status = %v, want open", got["goal_status"])
	}
}

func TestRun_resourceFinalize(t *testing.T) {
	workspaceDir := newBundle(t, validGoal)
	withFakePlect(t, workspaceDir)

	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	go func() {
		w.WriteString(`[{"id":"goal-met","reason":"checklist done"}]`)
		w.Close()
	}()
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	if err := run([]string{"resource", "finalize", "--resource", "local-okf://acme/goals/ship-it.md", "--revision", "sha256:abc"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	logged, err := os.ReadFile(filepath.Join(workspaceDir, "knowledge", "bundle", "log.md"))
	if err != nil {
		t.Fatalf("expected log.md to be written: %v", err)
	}
	if !bytes.Contains(logged, []byte("goal-met: checklist done")) {
		t.Errorf("log.md missing judge evidence:\n%s", logged)
	}
}

func TestRun_taskValidateGoalResource(t *testing.T) {
	if err := run([]string{"task", "validate-goal-resource", "--resource", "local-okf://acme/goals/ship-it.md"}); err != nil {
		t.Errorf("expected a valid goal resource to pass: %v", err)
	}
	if err := run([]string{"task", "validate-goal-resource", "--resource", "local-okf://acme/retrospectives/2026-q3.md"}); err == nil {
		t.Error("expected a non-goal resource to be rejected")
	}
}

func TestRun_unknownSubcommand(t *testing.T) {
	if err := run([]string{"resource", "explode"}); err == nil {
		t.Error("want an error for an unknown subcommand")
	}
}
