package adapter

import (
	"fmt"
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
		_, _ = toml.DecodeFile(configPath, cfg)
	}

	return cfg
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
