package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/plugins/github-provider/internal/workspace"
)

type fakeManager struct {
	adds      []workspace.AddParams
	removes   []removeCall
	addInfo   *workspace.WorkspaceInfo
	addErr    error
	findErr   error
	removeErr error
}

type removeCall struct {
	workdir      string
	gitDir       string
	branch       string
	force        bool
	deleteBranch bool
}

func (m *fakeManager) Add(ctx context.Context, params workspace.AddParams) (*workspace.WorkspaceInfo, error) {
	m.adds = append(m.adds, params)
	if m.addErr != nil {
		return nil, m.addErr
	}
	if m.addInfo != nil {
		return m.addInfo, nil
	}
	return &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-1"}, nil
}

func (m *fakeManager) FindGitDir(string, ...string) (string, error) {
	if m.findErr != nil {
		return "", m.findErr
	}
	return "/roots/src/acme/widgets", nil
}

func (m *fakeManager) RemoveByPath(ctx context.Context, workdir, gitDir, branch string, force, deleteBranch bool) error {
	m.removes = append(m.removes, removeCall{workdir: workdir, gitDir: gitDir, branch: branch, force: force, deleteBranch: deleteBranch})
	return m.removeErr
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
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/worktrees/github.com/acme/widgets/issue-42-review"}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/42",
		SessionName: "acme/widgets-42+review",
		Manager:     mgr,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(mgr.adds) != 1 {
		t.Fatalf("issued %d manager adds, want 1", len(mgr.adds))
	}
	add := mgr.adds[0]
	if add.Repo != "github.com/acme/widgets" || add.Branch != "issue/42+review" || add.BaseBranch != "issue/42" || add.SessionName != "acme/widgets-42+review" {
		t.Errorf("add params = %+v", add)
	}
	if add.FallbackRefspec != "" {
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
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-7"}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/7",
		SessionName: "acme/widgets-7",
		Manager:     mgr,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if outputs["branch"] != "issue/7" {
		t.Errorf("branch = %v, want issue/7", outputs["branch"])
	}
	if got := mgr.adds[0].Branch; got != "issue/7" {
		t.Errorf("branch = %q, want issue/7", got)
	}
}

func TestSetup_InvalidResource(t *testing.T) {
	mgr := &fakeManager{}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "not-a-github-resource",
		SessionName: "s",
		Manager:     mgr,
	})
	if err == nil {
		t.Fatal("expected an error for an unparsable resource identifier")
	}
	if len(mgr.adds) != 0 {
		t.Errorf("an unparsable resource must not reach the manager, got %v", mgr.adds)
	}
}

func TestSetup_AcquisitionFailurePropagates(t *testing.T) {
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Manager:     &fakeManager{addErr: errors.New("repository not found")},
	})
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("error = %v, want the acquisition failure to propagate", err)
	}
}

func TestSetup_MissingWorkdirPathIsAnError(t *testing.T) {
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Manager:     &fakeManager{addInfo: &workspace.WorkspaceInfo{}},
	})
	if err == nil || !strings.Contains(err.Error(), "workdir path") {
		t.Fatalf("error = %v, want a missing-workdir-path error", err)
	}
}

func TestCleanup_ReleasesWorktreeAndReclaimsBranch(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-42-review",
		Branch:  "issue/42+review",
		Manager: mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(mgr.removes) != 1 {
		t.Fatalf("issued %d manager removes, want 1", len(mgr.removes))
	}
	remove := mgr.removes[0]
	if remove.workdir != "/roots/wt/issue-42-review" || remove.branch != "issue/42+review" {
		t.Errorf("remove = %+v", remove)
	}
	if !remove.deleteBranch {
		t.Error("cleanup must reclaim the branch it acquired")
	}
}

func TestCleanup_ForcePassesForceFlagToWorkspaceRemove(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-42-review",
		Force:   true,
		Manager: mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !mgr.removes[0].force {
		t.Errorf("remove = %+v, want force passed through", mgr.removes[0])
	}
}

func TestCleanup_NoWorkdirIsANoOp(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{Manager: mgr}); err != nil {
		t.Fatalf("Cleanup with no workdir: %v", err)
	}
	if len(mgr.removes) != 0 {
		t.Errorf("nothing was acquired, so nothing may be released, got %v", mgr.removes)
	}
}

func TestCleanup_FailurePropagates(t *testing.T) {
	err := Cleanup(context.Background(), CleanupOptions{
		Workdir: "/roots/wt/issue-1",
		Manager: &fakeManager{removeErr: errors.New("worktree is dirty")},
	})
	if err == nil || !strings.Contains(err.Error(), "worktree is dirty") {
		t.Fatalf("error = %v, want the removal failure to propagate", err)
	}
}

func TestWorkdirsRootComesFromHookArgumentNotCoreConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "plect")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`workdirs_root = "/configured/root"`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := workdirsRoot(""), filepath.Join(home, "workdirs"); got != want {
		t.Fatalf("workdirsRoot(\"\") = %q, want default %q instead of config.toml", got, want)
	}
	if got, want := workdirsRoot("~/custom"), filepath.Join(home, "custom"); got != want {
		t.Fatalf("workdirsRoot(\"~/custom\") = %q, want %q", got, want)
	}
	if got := workdirsRoot("/explicit/root"); got != "/explicit/root" {
		t.Fatalf("workdirsRoot override = %q, want /explicit/root", got)
	}
}
