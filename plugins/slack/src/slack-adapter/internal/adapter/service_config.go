package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var tokenEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var slackChannelID = regexp.MustCompile(`^[A-Z0-9]+$`)

type Bot struct {
	AppToken             string   `json:"-"`
	BotToken             string   `json:"-"`
	AppTokenEnv          string   `json:"app_token_env"`
	BotTokenEnv          string   `json:"bot_token_env"`
	ChannelIDs           []string `json:"channel_ids"`
	AllowedUserIDs       []string `json:"allowed_user_ids"`
	DeniedUserMessage    string   `json:"denied_user_message,omitempty"`
	DeniedChannelMessage string   `json:"denied_channel_message,omitempty"`
}

type ServiceConfig struct {
	Bots       []Bot  `json:"bots"`
	SocketPath string `json:"socket_path,omitempty"`
}

func (b Bot) AllowsUser(userID string) bool {
	if len(b.AllowedUserIDs) == 0 {
		return true
	}
	for _, allowed := range b.AllowedUserIDs {
		if allowed == userID {
			return true
		}
	}
	return false
}

func (b Bot) AllowsChannel(channelID string) bool {
	for _, allowed := range b.ChannelIDs {
		if allowed == channelID {
			return true
		}
	}
	return false
}

func ParseServiceConfig(input []byte) (ServiceConfig, error) {
	var config ServiceConfig
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ServiceConfig{}, fmt.Errorf("service config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ServiceConfig{}, fmt.Errorf("service config: trailing value")
	}
	if len(config.Bots) == 0 {
		return ServiceConfig{}, fmt.Errorf("service config: bots must be nonempty")
	}
	if config.SocketPath != "" && !filepath.IsAbs(config.SocketPath) {
		return ServiceConfig{}, fmt.Errorf("service config: socket_path must be absolute")
	}
	seen := make(map[string]bool)
	apps := make(map[string]bool)
	botTokens := make(map[string]bool)
	for i := range config.Bots {
		bot := &config.Bots[i]
		if len(bot.ChannelIDs) == 0 || bot.AppTokenEnv == "" || bot.BotTokenEnv == "" || bot.AllowedUserIDs == nil {
			return ServiceConfig{}, fmt.Errorf("service config: bot %d requires app_token_env, bot_token_env, nonempty channel_ids, and allowed_user_ids", i)
		}
		for _, channelID := range bot.ChannelIDs {
			if !slackChannelID.MatchString(channelID) {
				return ServiceConfig{}, fmt.Errorf("service config: invalid channel_id %q", channelID)
			}
			if seen[channelID] {
				return ServiceConfig{}, fmt.Errorf("service config: duplicate channel_id %q", channelID)
			}
			seen[channelID] = true
		}
		for _, userID := range bot.AllowedUserIDs {
			if !slackChannelID.MatchString(userID) {
				return ServiceConfig{}, fmt.Errorf("service config: invalid allowed_user_ids entry %q", userID)
			}
		}
		for _, binding := range []struct {
			name  string
			value *string
		}{{bot.AppTokenEnv, &bot.AppToken}, {bot.BotTokenEnv, &bot.BotToken}} {
			if !tokenEnvName.MatchString(binding.name) {
				return ServiceConfig{}, fmt.Errorf("service config: invalid token environment name %q", binding.name)
			}
			*binding.value = os.Getenv(binding.name)
			if *binding.value == "" {
				return ServiceConfig{}, fmt.Errorf("service config: environment variable %s is empty or absent", binding.name)
			}
		}
		if apps[bot.AppToken] || botTokens[bot.BotToken] {
			return ServiceConfig{}, fmt.Errorf("service config: duplicate App credentials in bot %d", i)
		}
		apps[bot.AppToken] = true
		botTokens[bot.BotToken] = true
	}
	return config, nil
}
