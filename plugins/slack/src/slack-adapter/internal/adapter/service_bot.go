package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type resourcePublisher interface {
	Publish(context.Context, string, string, string, string, map[string]string) (int, error)
}

type permalinkResolver interface {
	permalink(string, string) (string, error)
}

var slackAPIURL = "https://slack.com/api/"

type BotAdapter struct {
	bot          Bot
	api          *slack.Client
	socket       *socketmode.Client
	teamID       string
	botUserID    string
	publisher    resourcePublisher
	fetcher      threadFetcher
	linker       permalinkResolver
	denials      *DenialStore
	logger       *slog.Logger
	recovery     *RecoveryStore
	recoveryPace time.Duration
	subs         *Subscriptions
	startup      time.Time
	seenMu       sync.Mutex
	seen         map[string]time.Time
}

func NewBotAdapter(bot Bot, publisher resourcePublisher, denials *DenialStore, logger *slog.Logger) (*BotAdapter, error) {
	api := slack.New(bot.BotToken, slack.OptionAPIURL(slackAPIURL), slack.OptionAppLevelToken(bot.AppToken), slack.OptionRetry(1))
	auth, err := api.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("auth.test for channel %s: %w", bot.ChannelID, err)
	}
	if auth.TeamID == "" || auth.UserID == "" {
		return nil, fmt.Errorf("auth.test for channel %s returned incomplete bot identity", bot.ChannelID)
	}
	b := &BotAdapter{bot: bot, api: api, socket: socketmode.New(api), teamID: auth.TeamID, botUserID: auth.UserID, publisher: publisher, denials: denials, logger: logger, recoveryPace: 1200 * time.Millisecond}
	b.fetcher = b
	b.linker = b
	return b, nil
}

func (b *BotAdapter) ConfigureRecovery(store *RecoveryStore, subs *Subscriptions) {
	b.recovery = store
	b.subs = subs
	b.startup = time.Now()
}

func (b *BotAdapter) Run(ctx context.Context) error {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		b.runEvents(ctx, b.socket.Events, ticker.C, func(ctx context.Context) error {
			return b.Recover(ctx, time.Now(), b.subs)
		})
	}()
	return b.socket.RunContext(ctx)
}

func (b *BotAdapter) runEvents(ctx context.Context, events <-chan socketmode.Event, ticks <-chan time.Time, scan func(context.Context) error) {
	connected := false
	scanning := false
	scanOK := false
	pendingScan := false
	scanDone := make(chan bool, 1)
	launchScan := func() {
		scanning = true
		go func() {
			err := scan(ctx)
			if err != nil {
				b.logger.Warn("recovery incomplete", "channel_id", b.bot.ChannelID, "error", err)
			}
			select {
			case scanDone <- err == nil:
			case <-ctx.Done():
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case scanOK = <-scanDone:
			scanning = false
			if pendingScan && connected {
				pendingScan = false
				scanOK = false
				launchScan()
			}
		case at := <-ticks:
			if connected && scanOK && b.recovery != nil {
				if err := b.recovery.SetBoundary(b.teamID+"/"+b.botUserID+"/"+b.bot.ChannelID, at); err != nil {
					b.logger.Warn("online position failed", "channel_id", b.bot.ChannelID, "error", err)
				}
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Type == socketmode.EventTypeConnected {
				connected = true
				scanOK = false
				if scanning {
					pendingScan = true
				} else {
					launchScan()
				}
				continue
			}
			if event.Type == socketmode.EventTypeDisconnect || event.Type == socketmode.EventTypeConnectionError {
				connected = false
			}
			if event.Type != socketmode.EventTypeEventsAPI {
				continue
			}
			if event.Request != nil {
				b.socket.Ack(*event.Request)
			}
			outer, ok := event.Data.(slackevents.EventsAPIEvent)
			if ok {
				b.handleEventsAPI(outer)
			}
		}
	}
}

func (b *BotAdapter) handleEventsAPI(outer slackevents.EventsAPIEvent) {
	if outer.Type != slackevents.CallbackEvent {
		return
	}
	switch ev := outer.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		b.routeMention(ev)
	case *slackevents.MessageEvent:
		if ev.BotID == "" && ev.SubType == "" && strings.Contains(ev.Text, "<@"+b.botUserID+">") {
			b.routeMention(&slackevents.AppMentionEvent{User: ev.User, Text: ev.Text, TimeStamp: ev.TimeStamp, ThreadTimeStamp: ev.ThreadTimeStamp, Channel: ev.Channel, UserTeam: ev.UserTeam})
		}
	}
}

func (b *BotAdapter) routeMention(ev *slackevents.AppMentionEvent) (bool, int) {
	if ev.BotID != "" || ev.TimeStamp == "" {
		return true, 0
	}
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}
	if !isSlackTimestampShape(threadTS) || !isSlackTimestampShape(ev.TimeStamp) {
		b.logger.Info("mention dropped", "channel_id", ev.Channel, "thread_ts", threadTS, "reason", "invalid_timestamp")
		return true, 0
	}
	if ev.Channel != b.bot.ChannelID {
		b.deny(ev, threadTS, "channel_not_allowed", b.bot.DeniedChannelMessage)
		return true, 0
	}
	if !b.bot.AllowsUser(ev.User) {
		b.deny(ev, threadTS, "user_not_allowed", b.bot.DeniedUserMessage)
		return true, 0
	}
	key := ev.Channel + "/" + ev.TimeStamp
	if !b.claimMention(key) {
		if b.recovery != nil && !b.recovery.Seen(b.teamID+"/"+b.botUserID+"/"+ev.Channel, ev.TimeStamp) {
			return false, 0
		}
		return true, 0
	}
	completed := false
	defer func() {
		if !completed {
			b.releaseMention(key)
		}
	}()
	storeKey := b.teamID + "/" + b.botUserID + "/" + ev.Channel
	if b.recovery != nil && b.recovery.Seen(storeKey, ev.TimeStamp) {
		return true, 0
	}
	finish := func(delivered int) (bool, int) {
		if b.recovery != nil {
			if err := b.recovery.Record(storeKey, ev.TimeStamp); err != nil {
				b.logger.Warn("mention record failed", "channel_id", ev.Channel, "message_ts", ev.TimeStamp, "error", err)
				return false, delivered
			}
		}
		completed = true
		return true, delivered
	}
	linker := b.linker
	if linker == nil {
		linker = b
	}
	resource, err := linker.permalink(ev.Channel, threadTS)
	if err != nil {
		b.logger.Warn("mention permalink failed", "channel_id", ev.Channel, "thread_ts", threadTS, "error", err)
		return false, 0
	}
	fetcher := b.fetcher
	if fetcher == nil {
		fetcher = b
	}
	names := withCachedNames(fetcher)
	replies, err := names.fetchThreadReplies(ev.Channel, threadTS)
	if err != nil {
		b.logger.Warn("mention transcript failed", "channel_id", ev.Channel, "thread_ts", threadTS, "error", err)
		return false, 0
	}
	user := names.userDisplayName(ev.User)
	if user == "" {
		user = ev.User
	}
	body, _, _ := buildDeliberationTranscript(names, replies, *ev, threadTS)
	metadata := map[string]string{"thread_ts": threadTS, "channel_id": ev.Channel, "mention_ts": ev.TimeStamp, "user_id": ev.User, "user": user}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	count, err := b.publisher.Publish(ctx, resource, "user.emit", "Slack deliberation from "+user, body, metadata)
	if err != nil {
		b.logger.Warn("mention publish failed", "channel_id", ev.Channel, "thread_ts", threadTS, "resource", resource, "error", err)
		return false, 0
	}
	if count > 0 {
		return finish(count)
	}
	botResource := "slack://" + b.teamID + "/" + b.botUserID
	count, err = b.publisher.Publish(ctx, botResource, "user.emit", "Slack deliberation from "+user, body, metadata)
	if err != nil {
		b.logger.Warn("mention publish failed", "channel_id", ev.Channel, "thread_ts", threadTS, "resource", botResource, "error", err)
		return false, 0
	}
	if count == 0 {
		b.logger.Info("mention dropped", "channel_id", ev.Channel, "thread_ts", threadTS, "reason", "no_subscriber")
	}
	return finish(count)
}

func (b *BotAdapter) claimMention(key string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if b.seen == nil {
		b.seen = make(map[string]time.Time)
	}
	if _, ok := b.seen[key]; ok {
		return false
	}
	if len(b.seen) >= 4096 {
		cutoff := time.Now().Add(-10 * time.Minute)
		for prior, at := range b.seen {
			if at.Before(cutoff) {
				delete(b.seen, prior)
			}
		}
		if len(b.seen) >= 4096 {
			var oldest string
			var at time.Time
			for prior, seenAt := range b.seen {
				if oldest == "" || seenAt.Before(at) {
					oldest, at = prior, seenAt
				}
			}
			delete(b.seen, oldest)
		}
	}
	b.seen[key] = time.Now()
	return true
}

func (b *BotAdapter) releaseMention(key string) {
	b.seenMu.Lock()
	delete(b.seen, key)
	b.seenMu.Unlock()
}

func (b *BotAdapter) deny(ev *slackevents.AppMentionEvent, threadTS, reason, message string) {
	b.logger.Info("mention denied", "channel_id", ev.Channel, "thread_ts", threadTS, "user_id", ev.User, "reason", reason)
	if message == "" || b.api == nil {
		return
	}
	if b.denials != nil {
		claimed, err := b.denials.Claim(b.botUserID, ev.Channel, threadTS, ev.User)
		if err != nil {
			b.logger.Warn("denial record failed", "error", err)
			return
		}
		if !claimed {
			return
		}
	}
	_, _, err := b.api.PostMessage(ev.Channel, slack.MsgOptionText(message, false), slack.MsgOptionTS(threadTS))
	if err != nil {
		if b.denials != nil {
			if releaseErr := b.denials.Release(b.botUserID, ev.Channel, threadTS, ev.User); releaseErr != nil {
				b.logger.Warn("denial record release failed", "error", releaseErr)
			}
		}
		b.logger.Warn("denial reply failed", "channel_id", ev.Channel, "thread_ts", threadTS, "error", err)
	}
}

func (b *BotAdapter) permalink(channelID, ts string) (string, error) {
	return b.api.GetPermalink(&slack.PermalinkParameters{Channel: channelID, Ts: ts})
}

func (b *BotAdapter) fetchThreadReplies(channelID, threadTS string) ([]slack.Message, error) {
	var all []slack.Message
	cursor := ""
	for {
		msgs, more, next, err := b.api.GetConversationReplies(&slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: threadTS, Cursor: cursor, Inclusive: true, Oldest: threadTS, Limit: 200})
		if err != nil {
			return nil, err
		}
		all = append(all, msgs...)
		if !more || next == "" {
			return all, nil
		}
		cursor = next
	}
}

func (b *BotAdapter) userDisplayName(userID string) string {
	if userID == "" {
		return ""
	}
	info, err := b.api.GetUserInfo(userID)
	if err != nil {
		return userID
	}
	if info.Profile.DisplayName != "" {
		return info.Profile.DisplayName
	}
	if info.RealName != "" {
		return info.RealName
	}
	return userID
}
