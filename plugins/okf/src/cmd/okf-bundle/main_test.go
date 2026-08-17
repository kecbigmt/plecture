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

func TestRun_providerSetupAndCleanup(t *testing.T) {
	workspaceDir := newBundle(t, validGoal)
	withFakePlect(t, workspaceDir)

	stdout, err := captureStdout(t, func() error {
		return run([]string{"provider", "setup", "--resource", "local-okf://acme/goals/ship-it.md", "--session", "acme/review-1"})
	})
	if err != nil {
		t.Fatalf("run setup: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	scratchWorkspaceDir, _ := got["workspace_dir"].(string)
	if scratchWorkspaceDir == "" {
		t.Fatal("provider setup produced no workspace_dir")
	}

	if err := run([]string{"provider", "cleanup", "--workspace-dir", scratchWorkspaceDir}); err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	if _, err := os.Lstat(scratchWorkspaceDir); !os.IsNotExist(err) {
		t.Errorf("scratch workspace directory still exists after cleanup: err=%v", err)
	}
}

func TestRun_unknownSubcommand(t *testing.T) {
	if err := run([]string{"provider", "explode"}); err == nil {
		t.Error("want an error for an unknown subcommand")
	}
}
