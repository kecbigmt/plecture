package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeRunner struct {
	workspaceDir string
	output       []byte
	err          error
}

func (f fakeRunner) Status(alias string) ([]byte, error) {
	if f.err != nil {
		return f.output, f.err
	}
	return []byte(fmt.Sprintf(`{"runtime":{"workspace_dir_path":%q,"workspace_dir_exists":true}}`, f.workspaceDir)), nil
}

func newBundle(t *testing.T) (workspaceDir string) {
	t.Helper()
	workspaceDir = t.TempDir()
	goalsDir := filepath.Join(workspaceDir, "knowledge", "bundle", "goals")
	if err := os.MkdirAll(goalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalsDir, "ship-it.md"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	return workspaceDir
}

func TestSetup_createsScratchWorkspaceDirWithKnowledgeSymlink(t *testing.T) {
	workspaceDir := newBundle(t)
	runner := fakeRunner{workspaceDir: workspaceDir}

	result, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if result.Owner != "acme" || result.ConceptID != "goals/ship-it.md" {
		t.Errorf("got owner=%q conceptID=%q", result.Owner, result.ConceptID)
	}

	link := filepath.Join(result.WorkspaceDir, "knowledge")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected a knowledge symlink: %v", err)
	}
	bundleRoot, _ := filepath.EvalSymlinks(filepath.Join(workspaceDir, "knowledge", "bundle"))
	if target != bundleRoot {
		t.Errorf("symlink target = %q, want %q", target, bundleRoot)
	}
}

func TestSetup_isRerunnableForTheSameSession(t *testing.T) {
	workspaceDir := newBundle(t)
	runner := fakeRunner{workspaceDir: workspaceDir}

	if _, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1"); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if _, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1"); err != nil {
		t.Fatalf("second setup: %v", err)
	}
}

func TestSetup_missingConceptFileIsAHardError(t *testing.T) {
	workspaceDir := newBundle(t)
	runner := fakeRunner{workspaceDir: workspaceDir}

	_, err := Setup(runner, "local-okf://acme/goals/missing.md", "acme/review-1")
	if err == nil {
		t.Fatal("want an error: setup has no state to fold a missing concept into")
	}
}

func TestSetup_noOrchestratorSessionIsAHardError(t *testing.T) {
	runner := fakeRunner{output: []byte("no such session"), err: fmt.Errorf("exit 1")}

	_, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1")
	if err == nil {
		t.Fatal("want an error")
	}
}

func TestCleanup_removesTheScratchDir(t *testing.T) {
	workspaceDir := newBundle(t)
	runner := fakeRunner{workspaceDir: workspaceDir}
	result, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if err := Cleanup(result.WorkspaceDir); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Lstat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Errorf("scratch workspaceDir still exists after cleanup: err=%v", err)
	}
}

func TestCleanup_alreadyGoneConverges(t *testing.T) {
	if err := Cleanup(filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Errorf("Cleanup of an already-gone workspaceDir must succeed, got: %v", err)
	}
}

func TestCleanup_emptyWorkspaceDirIsANoop(t *testing.T) {
	if err := Cleanup(""); err != nil {
		t.Errorf("Cleanup(\"\") must succeed, got: %v", err)
	}
}

func TestCleanup_refusesAPathOutsideScratchTree(t *testing.T) {
	notScratch := t.TempDir()
	if err := Cleanup(notScratch); err == nil {
		t.Fatal("want an error: cleanup must refuse to rm -rf a path outside .okf-workspaces/")
	}
	if _, err := os.Lstat(notScratch); err != nil {
		t.Fatalf("the refused directory must survive: %v", err)
	}
}

func TestCleanup_refusesASymlinkedEscapeOutOfScratchTree(t *testing.T) {
	workspaceDir := newBundle(t)
	runner := fakeRunner{workspaceDir: workspaceDir}
	result, err := Setup(runner, "local-okf://acme/goals/ship-it.md", "acme/review-1")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	outside := t.TempDir()
	fakeWorkspaceDir := filepath.Join(t.TempDir(), "fake")
	if err := os.Symlink(outside, fakeWorkspaceDir); err != nil {
		t.Fatal(err)
	}
	_ = result

	if err := Cleanup(fakeWorkspaceDir); err == nil {
		t.Fatal("want an error: a symlink resolving outside .okf-workspaces/ must be refused")
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("the escaped-to directory must survive: %v", err)
	}
}
