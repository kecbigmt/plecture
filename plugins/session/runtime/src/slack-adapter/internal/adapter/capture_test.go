package adapter

import (
	"slices"
	"testing"
)

func TestCaptureArgs(t *testing.T) {
	args := captureArgs("owner/repo-1", "slack.message", "slack", "inbound",
		"alice via Slack", "hello",
		map[string]string{"user": "alice", "thread_ts": "111.0", "blank": ""})

	if args[0] != "event" || args[1] != "publish" || args[2] != "owner/repo-1" {
		t.Fatalf("leading args = %v, want [event publish owner/repo-1 ...]", args[:3])
	}

	wantPairs := map[string]string{
		"--type":      "slack.message",
		"--source":    "slack",
		"--direction": "inbound",
		"--summary":   "alice via Slack",
		"--body":      "hello",
	}
	for flag, want := range wantPairs {
		if got := flagValue(args, flag); got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}

	// Non-empty meta is emitted; empty-valued meta is dropped.
	metas := allFlagValues(args, "--meta")
	if !slices.Contains(metas, "user=alice") || !slices.Contains(metas, "thread_ts=111.0") {
		t.Errorf("meta = %v, want user=alice and thread_ts=111.0", metas)
	}
	for _, m := range metas {
		if m == "blank=" {
			t.Errorf("empty-valued meta should be dropped, got %v", metas)
		}
	}
}

func flagValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func allFlagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}
