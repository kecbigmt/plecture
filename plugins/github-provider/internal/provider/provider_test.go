package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// recordingRunner captures the workspace calls the provider issues and
// replays a canned stdout, so the acquisition contract can be asserted
// without a repository on disk.
func recordingRunner(stdout string, err error, calls *[][]string) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, append([]string{name}, args...))
		return []byte(stdout), err
	}
}

func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestSessionTag(t *testing.T) {
	tests := []struct {
		name    string
		session string
		want    string
	}{
		{"untagged", "acme/widgets-42", ""},
		{"tagged", "acme/widgets-42+review", "review"},
		{"last separator wins", "acme/widgets-42+a+b", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionTag(tt.session); got != tt.want {
				t.Errorf("SessionTag(%q) = %q, want %q", tt.session, got, tt.want)
			}
		})
	}
}

func TestSetup_IssueAcquiresTaggedWorktree(t *testing.T) {
	var calls [][]string
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/42",
		SessionName: "acme/widgets-42+review",
		Runner:      recordingRunner(`{"worktree_path":"/roots/worktrees/github.com/acme/widgets/issue-42-review"}`, nil, &calls),
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("issued %d workspace calls, want 1", len(calls))
	}
	args := calls[0]
	if args[1] != "workspace" || args[2] != "add" {
		t.Errorf("call = %v, want a workspace add", args)
	}
	for flag, want := range map[string]string{
		"--repo":        "github.com/acme/widgets",
		"--branch":      "issue/42+review",
		"--base-branch": "issue/42",
		"--session":     "acme/widgets-42+review",
	} {
		if got, ok := argValue(args, flag); !ok || got != want {
			t.Errorf("%s = %q (present=%v), want %q", flag, got, ok, want)
		}
	}
	// An issue has no pull request head to fall back to.
	if _, ok := argValue(args, "--fallback-refspec"); ok {
		t.Error("an issue acquisition must not pass a fallback refspec")
	}

	want := map[string]any{
		"workdir":    "/roots/worktrees/github.com/acme/widgets/issue-42-review",
		"branch":     "issue/42+review",
		"url":        "https://github.com/acme/widgets/issues/42",
		"owner_repo": "acme/widgets",
		"owner":      "acme",
		"repo":       "widgets",
		"number":     42,
	}
	if !reflect.DeepEqual(outputs, want) {
		t.Errorf("outputs = %v, want %v", outputs, want)
	}
}

func TestSetup_UntaggedSessionKeepsBaseBranch(t *testing.T) {
	var calls [][]string
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/7",
		SessionName: "acme/widgets-7",
		Runner:      recordingRunner(`{"worktree_path":"/roots/wt/issue-7"}`, nil, &calls),
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if outputs["branch"] != "issue/7" {
		t.Errorf("branch = %v, want issue/7", outputs["branch"])
	}
	if got, _ := argValue(calls[0], "--branch"); got != "issue/7" {
		t.Errorf("--branch = %q, want issue/7", got)
	}
}

func TestSetup_InvalidResource(t *testing.T) {
	var calls [][]string
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "not-a-github-resource",
		SessionName: "s",
		Runner:      recordingRunner("", nil, &calls),
	})
	if err == nil {
		t.Fatal("expected an error for an unparsable resource identifier")
	}
	if len(calls) != 0 {
		t.Errorf("an unparsable resource must not reach the workspace, got %v", calls)
	}
}

func TestSetup_WorkspaceFailurePropagates(t *testing.T) {
	var calls [][]string
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Runner:      recordingRunner("", errors.New("repository not found"), &calls),
	})
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("error = %v, want the workspace failure to propagate", err)
	}
}

func TestSetup_MissingWorktreePathIsAnError(t *testing.T) {
	var calls [][]string
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Runner:      recordingRunner(`{}`, nil, &calls),
	})
	if err == nil || !strings.Contains(err.Error(), "worktree path") {
		t.Fatalf("error = %v, want a missing-worktree-path error", err)
	}
}

func TestSetup_UnparsableWorkspaceOutput(t *testing.T) {
	var calls [][]string
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Runner:      recordingRunner("not json", nil, &calls),
	})
	if err == nil || !strings.Contains(err.Error(), "parse workspace details") {
		t.Fatalf("error = %v, want a parse failure", err)
	}
}

func TestCleanup_ReleasesWorktreeAndReclaimsBranch(t *testing.T) {
	var calls [][]string
	if err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-42-review",
		Branch:  "issue/42+review",
		Runner:  recordingRunner("", nil, &calls),
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("issued %d workspace calls, want 1", len(calls))
	}
	args := calls[0]
	if args[1] != "workspace" || args[2] != "remove" {
		t.Errorf("call = %v, want a workspace remove", args)
	}
	if got, _ := argValue(args, "--path"); got != "/roots/wt/issue-42-review" {
		t.Errorf("--path = %q", got)
	}
	if got, _ := argValue(args, "--branch"); got != "issue/42+review" {
		t.Errorf("--branch = %q", got)
	}
	found := false
	for _, a := range args {
		if a == "--delete-branch" {
			found = true
		}
	}
	if !found {
		t.Error("cleanup must reclaim the branch it acquired")
	}
}

func TestCleanup_ForcePassesForceFlagToWorkspaceRemove(t *testing.T) {
	var calls [][]string
	if err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-42-review",
		Force:   true,
		Runner:  recordingRunner("", nil, &calls),
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	args := calls[0]
	found := false
	for _, a := range args {
		if a == "--force" {
			found = true
		}
	}
	if !found {
		t.Errorf("call = %v, want --force passed through to workspace remove", args)
	}
}

func TestCleanup_NoWorkdirIsANoOp(t *testing.T) {
	var calls [][]string
	if err := Cleanup(context.Background(), CleanupOptions{Runner: recordingRunner("", nil, &calls)}); err != nil {
		t.Fatalf("Cleanup with no workdir: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("nothing was acquired, so nothing may be released, got %v", calls)
	}
}

func TestCleanup_FailurePropagates(t *testing.T) {
	var calls [][]string
	err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-1",
		Runner:  recordingRunner("", errors.New("worktree is dirty"), &calls),
	})
	if err == nil || !strings.Contains(err.Error(), "worktree is dirty") {
		t.Fatalf("error = %v, want the removal failure to propagate", err)
	}
}
