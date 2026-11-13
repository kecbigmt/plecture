package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

var tokenEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var slackChannelID = regexp.MustCompile(`^[A-Z0-9]+$`)

type Bot struct {
	AppToken             string   `json:"-"`
	BotToken             string   `json:"-"`
	AppTokenEnv          string   `json:"app_token_env"`
	BotTokenEnv          string   `json:"bot_token_env"`
	ChannelID            string   `json:"channel_id"`
	AllowedUserIDs       []string `json:"allowed_user_ids"`
	DeniedUserMessage    string   `json:"denied_user_message,omitempty"`
	DeniedChannelMessage string   `json:"denied_channel_message,omitempty"`
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

func ParseServiceConfig(input []byte) ([]Bot, error) {
	var config struct {
		Bots []Bot `json:"bots"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("service config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("service config: trailing value")
	}
	if len(config.Bots) == 0 {
		return nil, fmt.Errorf("service config: bots must be nonempty")
	}
	seen := make(map[string]bool)
	for i := range config.Bots {
		bot := &config.Bots[i]
		if bot.ChannelID == "" || bot.AppTokenEnv == "" || bot.BotTokenEnv == "" || bot.AllowedUserIDs == nil {
			return nil, fmt.Errorf("service config: bot %d requires app_token_env, bot_token_env, channel_id, and allowed_user_ids", i)
		}
		if !slackChannelID.MatchString(bot.ChannelID) {
			return nil, fmt.Errorf("service config: invalid channel_id %q", bot.ChannelID)
		}
		if seen[bot.ChannelID] {
			return nil, fmt.Errorf("service config: duplicate channel_id %q", bot.ChannelID)
		}
		seen[bot.ChannelID] = true
		for _, userID := range bot.AllowedUserIDs {
			if !slackChannelID.MatchString(userID) {
				return nil, fmt.Errorf("service config: invalid allowed_user_ids entry %q", userID)
			}
		}
		for _, binding := range []struct {
			name  string
			value *string
		}{{bot.AppTokenEnv, &bot.AppToken}, {bot.BotTokenEnv, &bot.BotToken}} {
			if !tokenEnvName.MatchString(binding.name) {
				return nil, fmt.Errorf("service config: invalid token environment name %q", binding.name)
			}
			*binding.value = os.Getenv(binding.name)
			if *binding.value == "" {
				return nil, fmt.Errorf("service config: environment variable %s is empty or absent", binding.name)
			}
		}
	}
	return config.Bots, nil
}
