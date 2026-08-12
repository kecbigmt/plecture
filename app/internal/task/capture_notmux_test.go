package task

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCapture_NoTmuxKnowledgeInCore locks in the capture design's core
// premise (mirroring attach): tmux knowledge — e.g. the "capture-pane"
// verb — must live only in declared task config
// (config/plect/tasks/tmux.toml), never hardcoded in plect core Go source. Core
// only renders whatever template a task definition declares and executes it
// via bash -c (RunCapture); it never knows it might be talking to tmux.
func TestCapture_NoTmuxKnowledgeInCore(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file: app/internal/task/capture_notmux_test.go -> walk from app/
	appDir := filepath.Join(filepath.Dir(thisFile), "..", "..")

	err := filepath.WalkDir(appDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "capture-pane") {
			t.Errorf("%s hardcodes tmux's capture-pane; capture's tmux knowledge belongs only in declared task config", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", appDir, err)
	}
}
