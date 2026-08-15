package channel

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/contracts/event"
)

// TestShippedCatalog_ChannelsRender renders every channel definition shipped
// by this repository's official catalog (agent/runtime, agent/claude,
// agent/codex, channel/slack) against a sample event, so a template typo in
// one of those channels' command/args/path/body fails CI instead of only
// surfacing at delivery.
func TestShippedCatalog_ChannelsRender(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	dirs := []string{
		filepath.Join(repoRoot, "plugins", "agent", "runtime"),
		filepath.Join(repoRoot, "plugins", "agent", "claude"),
		filepath.Join(repoRoot, "plugins", "agent", "codex"),
		filepath.Join(repoRoot, "plugins", "channel", "slack"),
	}
	channels, err := (&config.Config{PluginDirs: dirs}).LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	for _, id := range []string{"tmux_send_keys", "claude", "codex_exec", "slack"} {
		if _, ok := channels[id]; !ok {
			t.Errorf("shipped catalog channel %q not found", id)
		}
	}

	rctx := newRenderContext(
		map[string]any{
			"path":       "/run/claude-channel/x.sock",
			"session":    "test-session",
			"thread_ts":  "12.34",
			"channel_id": "C0",
			"base_url":   "http://127.0.0.1:7890",
			"queue_dir":  "/tmp/plect-codex-exec/test-session/queue",
		},
		event.Event{Type: "github.ci_status", Summary: "CI failed: test"},
	)
	for id, def := range channels {
		fields := map[string]string{"path": def.Path, "body": def.Body, "command": def.Command}
		for i, a := range def.Args {
			fields[fmt.Sprintf("args[%d]", i)] = a
		}
		for name, tmpl := range fields {
			if tmpl == "" {
				continue
			}
			if _, rerr := renderField(name, tmpl, rctx); rerr != nil {
				t.Errorf("channel %q %s render: %v", id, name, rerr)
			}
		}
	}
}
