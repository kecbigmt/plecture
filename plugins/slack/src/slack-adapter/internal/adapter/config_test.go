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

func TestLoadConfig_EnvVarsFillCredentialsWhenConfigFileAbsent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-env")
	t.Setenv("SLACK_APP_TOKEN", "xapp-env")
	t.Setenv("SLACK_CHANNEL_ID", "C-env")

	cfg := LoadConfig()

	if cfg.SlackBotToken != "xoxb-env" {
		t.Errorf("SlackBotToken = %q, want the env var value", cfg.SlackBotToken)
	}
	if cfg.SlackAppToken != "xapp-env" {
		t.Errorf("SlackAppToken = %q, want the env var value", cfg.SlackAppToken)
	}
	if cfg.ChannelID != "C-env" {
		t.Errorf("ChannelID = %q, want the env var value", cfg.ChannelID)
	}
}

func TestLoadConfig_ConfigFileTakesPriorityOverEnvVars(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-env")

	configDir := filepath.Join(tmpHome, ".config", "slack-adapter")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "slack_bot_token = \"xoxb-file\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadConfig()

	// config.toml is the file a user already fully controls; an env var
	// (typically supplied for resident-supervised startup) only fills a field
	// config.toml left empty, so a value explicitly set in the file is
	// never silently overridden by whatever happens to be in the process
	// environment.
	if cfg.SlackBotToken != "xoxb-file" {
		t.Errorf("SlackBotToken = %q, want the config.toml value to take priority", cfg.SlackBotToken)
	}
}

func TestValidateStartup_AllowsOutboundOnlyConfig(t *testing.T) {
	cfg := &Config{SlackBotToken: "xoxb-test"}

	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("ValidateStartup() error = %v, want nil", err)
	}
}

func TestValidateStartup_RequiresBotToken(t *testing.T) {
	cfg := &Config{SlackAppToken: "xapp-test", ChannelID: "C123"}

	if err := cfg.ValidateStartup(); err == nil {
		t.Fatal("ValidateStartup() error = nil, want missing bot token error")
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

func TestIsMentionUserAllowedDefaultsToAnyMember(t *testing.T) {
	cfg := &Config{}

	if !cfg.IsMentionUserAllowed("U12345") {
		t.Fatal("empty allowed_user_ids should allow app mentions")
	}
}

func TestIsMentionUserAllowedHonorsAllowListWhenSet(t *testing.T) {
	cfg := &Config{AllowedUserIDs: []string{"U-allowed"}}

	if !cfg.IsMentionUserAllowed("U-allowed") {
		t.Fatal("configured allowed user should be allowed")
	}
	if cfg.IsMentionUserAllowed("U-other") {
		t.Fatal("user outside configured allow-list should be rejected")
	}
}
