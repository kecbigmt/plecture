package webui

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// The control plane must not bind every interface by default; exposing it
// (private-network IP / 0.0.0.0) is an explicit opt-in via config or -host/-port.
func TestLoadConfig_DefaultLoopback(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no config file present -> defaults
	if got := LoadConfig().ListenAddr; got != "127.0.0.1:8787" {
		t.Errorf("default ListenAddr = %q, want 127.0.0.1:8787", got)
	}
}

func TestLoadConfig_MalformedConfigFileFallsBackAndWarns(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := filepath.Join(tmpHome, ".config", "plect-web")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("not = [valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	cfg := LoadConfig()

	if cfg.ListenAddr != "127.0.0.1:8787" {
		t.Errorf("ListenAddr = %q, want default", cfg.ListenAddr)
	}
	if !bytes.Contains(logs.Bytes(), []byte("config.toml present but failed to parse")) {
		t.Errorf("expected a warning about the unparsable config.toml, got log output: %q", logs.String())
	}
}

func TestConfig_Addr(t *testing.T) {
	c := &Config{ListenAddr: "127.0.0.1:8787"}
	// host/port overrides; empty keeps the configured value. Setting host is
	// how the UI gets exposed (private-network IP or 0.0.0.0).
	tests := []struct{ host, port, want string }{
		{"", "", "127.0.0.1:8787"},
		{"100.122.130.103", "", "100.122.130.103:8787"},
		{"", "9000", "127.0.0.1:9000"},
		{"0.0.0.0", "9000", "0.0.0.0:9000"},
	}
	for _, tt := range tests {
		if got := c.Addr(tt.host, tt.port); got != tt.want {
			t.Errorf("Addr(%q, %q) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}
