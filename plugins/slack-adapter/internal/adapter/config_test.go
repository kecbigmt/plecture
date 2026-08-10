package adapter

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_NoConfigFileUsesDefaultsSilently(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	cfg := LoadConfig()

	if cfg.ListenAddr != "127.0.0.1:7890" {
		t.Errorf("ListenAddr = %q, want default", cfg.ListenAddr)
	}
	if logs.Len() != 0 {
		t.Errorf("expected no warning for an absent config file, got: %q", logs.String())
	}
}

func TestLoadConfig_MalformedConfigFileFallsBackAndWarns(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := filepath.Join(tmpHome, ".config", "slack-adapter")
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

	if cfg.ListenAddr != "127.0.0.1:7890" {
		t.Errorf("ListenAddr = %q, want default", cfg.ListenAddr)
	}
	if !bytes.Contains(logs.Bytes(), []byte("config.toml present but failed to parse")) {
		t.Errorf("expected a warning about the unparsable config.toml, got log output: %q", logs.String())
	}
}

func TestMentionPrefix(t *testing.T) {
	tests := []struct {
		name           string
		notifyUserIDs  []string
		expectedPrefix string
	}{
		{
			name:           "no users configured",
			notifyUserIDs:  nil,
			expectedPrefix: "",
		},
		{
			name:           "single user",
			notifyUserIDs:  []string{"U12345"},
			expectedPrefix: "<@U12345> ",
		},
		{
			name:           "multiple users",
			notifyUserIDs:  []string{"U12345", "U67890"},
			expectedPrefix: "<@U12345> <@U67890> ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{NotifyUserIDs: tt.notifyUserIDs}
			got := cfg.MentionPrefix()
			if got != tt.expectedPrefix {
				t.Errorf("MentionPrefix() = %q, want %q", got, tt.expectedPrefix)
			}
		})
	}
}
