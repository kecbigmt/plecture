package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Adapter receives Slack activity and publishes it to subscribed sessions.
type Adapter struct {
	cfg            *Config
	api            *slack.Client
	sessionAPI     *slackSessionClient
	sm             *socketmode.Client
	workspace      string
	teamID         string
	botUserID      string
	broker         *Broker
	poster         ThreadPoster
	threader       ThreadCreator
	threadFetcher  threadFetcher
	eventPublisher eventPublisher
	statusManager  *StatusManager
	streamManager  *StreamManager
	logger         *slog.Logger

	permalinkResolver permalinkResolver
	mentionHook       mentionHookRunner
	mentions          *mentionStream
}

// SubscribersStatePath resolves $XDG_STATE_HOME/slack-adapter/subscribers.json,
// falling back to ~/.local/state/.../subscribers.json. Empty disables persistence.
func SubscribersStatePath() string {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "slack-adapter", "subscribers.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "slack-adapter", "subscribers.json")
}

// StreamStatePath resolves $XDG_STATE_HOME/slack-adapter/streams.json,
// falling back to ~/.local/state/.../streams.json. Empty disables persistence.
func StreamStatePath() string {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "slack-adapter", "streams.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "slack-adapter", "streams.json")
}

// New creates a new Adapter.
func New(cfg *Config, logger *slog.Logger) (*Adapter, error) {
	broker := NewBroker(SubscribersStatePath(), logger)
	if broker.loadErr != nil {
		return nil, broker.loadErr
	}
	options := []slack.Option{}
	if cfg.SlackAppToken != "" {
		options = append(options, slack.OptionAppLevelToken(cfg.SlackAppToken))
	}
	api := slack.New(cfg.SlackBotToken, options...)

	a := &Adapter{
		cfg:        cfg,
		api:        api,
		sessionAPI: newSlackSessionClient(cfg.SlackBotToken, "https://slack.com/api/", nil),
		broker:     broker,
		logger:     logger,
	}
	if cfg.SlackAppToken != "" {
		a.sm = socketmode.New(api)
	}
	a.poster = a
	a.threader = a
	a.threadFetcher = a
	a.eventPublisher = cliEventPublisher{}
	a.permalinkResolver = a
	a.mentionHook = cliMentionHookRunner{}
	a.mentions = newMentionStream()
	a.statusManager = NewStatusManager(a.poster, cfg.StatusTTLDuration(), logger)
	// Cache workspace name from Slack API
	resp, err := api.AuthTest()
	if err != nil {
		logger.Warn("AuthTest failed, workspace info unavailable", "error", err)
	} else if resp.URL != "" {
		if u, err := url.Parse(resp.URL); err == nil {
			parts := strings.Split(u.Hostname(), ".")
			if len(parts) > 0 {
				a.workspace = parts[0]
				logger.Info("workspace resolved", "workspace", a.workspace)
			}
		}
		a.teamID = resp.TeamID
		a.botUserID = resp.UserID
	}
	a.streamManager = NewStreamManagerWithStatePath(a, a.poster, logger, StreamStatePath())

	return a, nil
}

// Close releases the adapter's background resources.
func (a *Adapter) Close() {
	if a.statusManager != nil {
		a.statusManager.Stop()
	}
}

// Run starts the Slack Socket Mode listener. Blocks until ctx is cancelled.
func (a *Adapter) Run(ctx context.Context) error {
	if a.sm == nil {
		<-ctx.Done()
		return nil
	}
	go a.handleSlackEvents(ctx)
	return a.sm.RunContext(ctx)
}

func (a *Adapter) handleSlackEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-a.sm.Events:
			if !ok {
				return
			}
			a.processEvent(evt)
		}
	}
}

func (a *Adapter) processEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		a.sm.Ack(*evt.Request)
		eventsAPI, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		a.handleEventsAPI(eventsAPI)
	case socketmode.EventTypeConnecting:
		a.logger.Info("connecting to Slack")
	case socketmode.EventTypeConnected:
		a.logger.Info("connected to Slack")
	case socketmode.EventTypeConnectionError:
		a.logger.Error("connection error",
			"component", "slack-adapter",
			"event", "slack_connection_error",
			"error", fmt.Sprintf("%v", evt.Data),
		)
	default:
		a.logger.Debug("unhandled event type", "type", evt.Type)
	}
}

func (a *Adapter) handleEventsAPI(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		switch ev := event.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			a.handleMessage(ev)
		case *slackevents.AppMentionEvent:
			a.logger.Info("received app mention", "user", ev.User, "bot_id", ev.BotID, "thread_ts", ev.ThreadTimeStamp, "ts", ev.TimeStamp, "channel", ev.Channel)
			a.handleAppMention(ev)
		}
	}
}

func (a *Adapter) handleMessage(ev *slackevents.MessageEvent) {
	a.logger.Info("received message",
		"user", ev.User, "bot_id", ev.BotID, "sub_type", ev.SubType,
		"thread_ts", ev.ThreadTimeStamp, "channel", ev.Channel)

	// Ignore bot messages and subtypes (edits, deletes, etc.)
	if ev.BotID != "" || ev.SubType != "" {
		a.logger.Debug("dropping message", "reason", "bot_or_subtype", "bot_id", ev.BotID, "sub_type", ev.SubType)
		return
	}

	// Sender gating
	if !a.cfg.IsMentionUserAllowed(ev.User) {
		a.logger.Debug("dropping message", "reason", "user_not_allowed", "user", ev.User)
		return
	}

	if a.isAppMentionMessage(ev.Text) {
		if a.botUserID == "" {
			a.logger.Info("message mention skipped", "reason", "bot_identity_unknown", "thread_ts", ev.ThreadTimeStamp, "ts", ev.TimeStamp, "channel_id", ev.Channel)
			return
		}
		if ev.TimeStamp == "" {
			a.logger.Warn("cannot deliver message mention without timestamp", "thread_ts", ev.ThreadTimeStamp, "channel_id", ev.Channel)
			return
		}
		a.logger.Info("received message mention", "user", ev.User, "thread_ts", ev.ThreadTimeStamp, "ts", ev.TimeStamp, "channel", ev.Channel)
		a.handleAppMention(&slackevents.AppMentionEvent{
			User: ev.User, Text: ev.Text, TimeStamp: ev.TimeStamp,
			ThreadTimeStamp: ev.ThreadTimeStamp, Channel: ev.Channel, UserTeam: ev.UserTeam,
		})
		return
	}

	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		a.logger.Debug("dropping message", "reason", "not_thread_reply")
		return
	}

	// Resolve display name
	userName := ev.User
	info, err := a.api.GetUserInfo(ev.User)
	if err == nil {
		userName = info.Profile.DisplayName
		if userName == "" {
			userName = info.RealName
		}
	}

	delivered := false
	for _, sub := range a.broker.ListThread(threadTS) {
		if !subscribedThreadInChannel(sub, ev.Channel) {
			continue
		}
		if a.publishInboundReply(sub, userName, ev.User, ev.Text) == nil {
			delivered = true
		}
	}
	if delivered {
		a.recordStreamRecipient(ev.Channel, threadTS, ev.User, ev.UserTeam)
	}
}

func subscribedThreadInChannel(sub Subscriber, channelID string) bool {
	return sub.SessionName != "" && sub.ChannelID == channelID
}

func (a *Adapter) isAppMentionMessage(text string) bool {
	if !strings.Contains(text, "<@") {
		return false
	}
	if a.botUserID == "" {
		if a.api == nil {
			return true
		}
		auth, err := a.api.AuthTest()
		if err != nil || auth.UserID == "" {
			a.logger.Warn("cannot classify message with a mention without bot identity", "error", err)
			return true
		}
		a.botUserID = auth.UserID
	}
	return strings.Contains(text, "<@"+a.botUserID+">")
}

// recordStreamRecipient makes the sender of an inbound message that actually
// reached its session the addressee of one of the thread's next streams: a
// message that never ran a turn must not claim a later reply. A sender from another workspace of
// a shared channel is addressed in their own workspace; the Slack event
// names that workspace only for such senders, so any other falls back to
// the app's own.
func (a *Adapter) recordStreamRecipient(channelID, threadTS, userID, userTeam string) {
	if a.streamManager == nil {
		return
	}
	if userTeam == "" {
		userTeam = a.teamID
	}
	a.streamManager.RecordRecipient(channelID, threadTS, userID, userTeam)
}

// PostToThread posts a message to a Slack thread.
func (a *Adapter) PostToThread(channelID, threadTS, text string) (string, error) {
	_, ts, err := a.api.PostMessage(
		channelID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	return ts, err
}

func (a *Adapter) SetThreadStatus(channelID, threadTS, status string) error {
	return a.sessionAPI.SetStatus(channelID, threadTS, status)
}

// CreateThread posts a new message to a channel and returns Slack's permalink.
func (a *Adapter) CreateThread(channelID, text string) (string, string, error) {
	_, ts, err := a.api.PostMessage(
		channelID,
		slack.MsgOptionText(text, false),
	)
	if err != nil {
		return "", "", err
	}
	permalink, err := a.api.GetPermalink(&slack.PermalinkParameters{
		Channel: channelID,
		Ts:      ts,
	})
	if err != nil {
		return "", "", err
	}
	return ts, permalink, nil
}

type ThreadPoster interface {
	PostToThread(channelID, threadTS, text string) (string, error)
	ThreadStatusSetter
}

type ThreadCreator interface {
	CreateThread(channelID, text string) (threadTS, permalink string, err error)
}
