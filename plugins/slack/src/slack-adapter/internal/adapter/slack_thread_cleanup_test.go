package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSlackThreadCleanupHandlesCurlFailures(t *testing.T) {
	var task struct {
		SlackThread struct {
			Cleanup struct {
				Script string
			} `toml:"cleanup"`
		} `toml:"slack_thread"`
	}
	path := filepath.Join("..", "..", "..", "..", "config", "tasks", "slack_thread.toml")
	if _, err := toml.DecodeFile(path, &task); err != nil {
		t.Fatal(err)
	}
	if task.SlackThread.Cleanup.Script == "" {
		t.Fatal("slack_thread cleanup script is empty")
	}
	for _, tc := range []struct {
		name, exitCode string
		wantSuccess    bool
	}{
		{name: "adapter unreachable", exitCode: "7", wantSuccess: true},
		{name: "request rejected", exitCode: "22", wantSuccess: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nexit "+tc.exitCode+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", task.SlackThread.Cleanup.Script)
			cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "base_url=http://127.0.0.1:1", "session_name=one", "thread_ts=1700000000.000001")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("cleanup error = %v, want success %v; output: %s", err, tc.wantSuccess, out)
			}
			if tc.wantSuccess && !strings.Contains(string(out), "unsubscribe failed") {
				t.Fatalf("cleanup did not log the failed unsubscribe: %s", out)
			}
		})
	}
}
