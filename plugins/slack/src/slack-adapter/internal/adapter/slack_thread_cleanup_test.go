package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSlackThreadCreatesThreadWithoutSubscribing(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable")
	}
	var task struct {
		SlackThread struct {
			Setup   struct{ Script string } `toml:"setup"`
			Cleanup *struct{}               `toml:"cleanup"`
		} `toml:"slack_thread"`
	}
	path := filepath.Join("..", "..", "..", "..", "config", "tasks", "slack_thread.toml")
	if _, err := toml.DecodeFile(path, &task); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/bin/sh\nprintf '%s' '{\"thread_ts\":\"1234.5678\",\"channel_id\":\"C123\",\"permalink\":\"https://example.slack.com/archives/C123/p12345678\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(t.TempDir(), "plect-called")
	if err := os.WriteFile(filepath.Join(binDir, "plect"), []byte("#!/bin/sh\ntouch '"+called+"'\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", task.SlackThread.Setup.Script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "base_url=http://127.0.0.1:1", "channel_id=C123", "root_text=Review", "session_name=one")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"permalink":"https://example.slack.com/archives/C123/p12345678"`) {
		t.Fatalf("setup output = %q, error = %v", out, err)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("setup invoked plect: %v", err)
	}
	if task.SlackThread.Cleanup != nil {
		t.Fatal("thread creation must not own subscription cleanup")
	}
}
