package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSessionMcpListenSocket(t *testing.T) {
	t.Run("under XDG_RUNTIME_DIR", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		got := defaultSessionMcpListenSocket("acme/widgets-758+claude")
		want := "/run/user/1000/plecture-mcp/acme/widgets-758+claude.sock"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back to os.TempDir without XDG_RUNTIME_DIR", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		got := defaultSessionMcpListenSocket("owner/session")
		want := filepath.Join(os.TempDir(), "plecture-mcp", "owner/session.sock")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
