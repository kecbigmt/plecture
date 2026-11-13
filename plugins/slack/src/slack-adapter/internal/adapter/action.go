package adapter

import (
	"fmt"

	"github.com/slack-go/slack"
)

type Action struct {
	channel string
	api     *slack.Client
	status  *slackSessionClient
}

func NewAction(bots []Bot, channelID, apiURL string) (*Action, error) {
	for _, bot := range bots {
		if bot.ChannelID != channelID {
			continue
		}
		return &Action{channel: channelID, api: slack.New(bot.BotToken, slack.OptionAPIURL(apiURL)), status: newSlackSessionClient(bot.BotToken, apiURL, nil)}, nil
	}
	return nil, fmt.Errorf("no configured Slack bot for channel %q", channelID)
}

func (a *Action) CreateThread(text string) (string, string, error) {
	_, ts, err := a.api.PostMessage(a.channel, slack.MsgOptionText(text, false))
	if err != nil {
		return "", "", err
	}
	link, err := a.api.GetPermalink(&slack.PermalinkParameters{Channel: a.channel, Ts: ts})
	return ts, link, err
}

func (a *Action) PostMessage(threadTS, text string) error {
	_, _, err := a.api.PostMessage(a.channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))
	return err
}

func (a *Action) SetStatus(threadTS, status string) error {
	if status != "" {
		status = "processing"
	}
	return a.status.SetStatus(a.channel, threadTS, status)
}
