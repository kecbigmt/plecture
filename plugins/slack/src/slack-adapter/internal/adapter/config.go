package adapter

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	SlackBotToken  string   `toml:"slack_bot_token"`
	SlackAppToken  string   `toml:"slack_app_token"`
	ChannelID      string   `toml:"channel_id"`
	ListenAddr     string   `toml:"listen_addr"`
	AllowedUserIDs []string `toml:"allowed_user_ids"`
	NotifyUserIDs  []string `toml:"notify_user_ids"`
}

func LoadConfig() *Config {
	cfg := &Config{
		ListenAddr: "127.0.0.1:7890",
	}

	home, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(home, ".config", "slack-adapter", "config.toml")
		if _, statErr := os.Stat(configPath); statErr == nil {
			// An absent config.toml is a normal all-env-vars setup, but a
			// present-and-unparsable one is a user mistake that would
			// otherwise silently fall back to an empty (deny-all) config —
			// warn so it isn't mistaken for "everything is configured".
			if _, decodeErr := toml.DecodeFile(configPath, cfg); decodeErr != nil {
				slog.Warn("config.toml present but failed to parse; using defaults", "path", configPath, "error", decodeErr)
			}
		}
	}

	// Env vars only fill a field config.toml left empty: config.toml is a
	// file the user already fully controls, so an explicit value there is
	// never silently overridden by whatever happens to be in the process
	// environment. This is the credential path a resident-supervised
	// [[services]] declaration's `required_env` gates on (see this
	// plugin's plugin.toml) — plect never writes secrets into catalog
	// content, so they arrive as environment instead.
	fillFromEnv(&cfg.SlackBotToken, "SLACK_BOT_TOKEN")
	fillFromEnv(&cfg.SlackAppToken, "SLACK_APP_TOKEN")
	fillFromEnv(&cfg.ChannelID, "SLACK_CHANNEL_ID")

	return cfg
}

func (c *Config) ValidateStartup() error {
	if c.SlackBotToken == "" {
		return errors.New("slack_bot_token must be set in config")
	}
	return nil
}

func fillFromEnv(field *string, envVar string) {
	if *field != "" {
		return
	}
	if v := os.Getenv(envVar); v != "" {
		*field = v
	}
}

func (c *Config) IsUserAllowed(userID string) bool {
	if len(c.AllowedUserIDs) == 0 {
		return false // deny all if no allowlist configured
	}
	for _, id := range c.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// MentionPrefix returns a string of Slack user mentions (e.g. "<@U123> <@U456> ")
// for use as a message prefix. Returns empty string if no notify_user_ids are configured.
func (c *Config) MentionPrefix() string {
	if len(c.NotifyUserIDs) == 0 {
		return ""
	}
	var parts []string
	for _, id := range c.NotifyUserIDs {
		parts = append(parts, fmt.Sprintf("<@%s>", id))
	}
	return strings.Join(parts, " ") + " "
}
