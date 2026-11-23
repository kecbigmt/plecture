package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// mergedWorktree lays out a primary checkout and one worktree on a branch
// already merged into main, the way setup leaves a finished session.
func mergedWorktree(t *testing.T) (layoutRoot, workspaceDir, branch string) {
	t.Helper()
	layoutRoot = t.TempDir()
	repo := filepath.Join(layoutRoot, "github.com", "acme", "widgets")
	primary := filepath.Join(repo, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, primary, "init", "-b", "main")
	gitT(t, primary, "config", "user.email", "test@example.com")
	gitT(t, primary, "config", "user.name", "Test")
	gitT(t, primary, "commit", "--allow-empty", "-m", "init")
	branch = "issue/42"
	workspaceDir = filepath.Join(repo, "issue-42")
	gitT(t, primary, "worktree", "add", "-b", branch, workspaceDir)
	return layoutRoot, workspaceDir, branch
}

func TestCleanup_DeletesTheMergedBranchOnlyWhenTheRecordedPolicySaysSo(t *testing.T) {
	tests := []struct {
		name       string
		policy     []string
		wantBranch bool
	}{
		{name: "delete_branch = true", policy: []string{"--delete-branch=true"}, wantBranch: false},
		{name: "delete_branch rewritten to false", policy: []string{"--delete-branch=false"}, wantBranch: true},
		{name: "no policy recorded", wantBranch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layoutRoot, workspaceDir, branch := mergedWorktree(t)
			args := append([]string{
				"cleanup",
				"--workspace-dir", workspaceDir,
				"--branch", branch,
				"--workspace-layout-root", layoutRoot,
			}, tt.policy...)
			if err := run(args); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			if _, err := os.Stat(workspaceDir); !os.IsNotExist(err) {
				t.Errorf("worktree still present: %v", err)
			}
			primary := filepath.Join(layoutRoot, "github.com", "acme", "widgets", "main")
			kept := gitT(t, primary, "branch", "--list", branch) != ""
			if kept != tt.wantBranch {
				t.Errorf("branch %q kept = %v, want %v", branch, kept, tt.wantBranch)
			}
		})
	}
}

func TestParseTokenCommand(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "unset", raw: ""},
		{name: "the effect's empty default", raw: "[]"},
		{name: "argument array", raw: `["/usr/local/bin/installation-token","get"]`, want: []string{"/usr/local/bin/installation-token", "get"}},
		{name: "not an array", raw: `"/usr/local/bin/installation-token get"`, wantErr: true},
		{name: "empty argument", raw: `["/usr/local/bin/installation-token",""]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTokenCommand(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "--token-command") {
				t.Errorf("error = %v, want it to name --token-command", err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") || (got == nil) != (tt.want == nil) {
				t.Errorf("parseTokenCommand(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
