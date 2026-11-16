package adapter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func slackTime(at time.Time) string { return fmt.Sprintf("%d.%06d", at.Unix(), at.Nanosecond()/1000) }

func (b *BotAdapter) Recover(ctx context.Context, now time.Time, subs *Subscriptions) error {
	if b.recovery == nil {
		return nil
	}
	for _, channelID := range b.bot.ChannelIDs {
		if err := b.recoverChannel(ctx, now, subs, channelID); err != nil {
			return err
		}
	}
	return nil
}

func (b *BotAdapter) recoverChannel(ctx context.Context, now time.Time, subs *Subscriptions, channelID string) error {
	recovered := 0
	defer func() { b.logger.Info(fmt.Sprintf("recovered %d mentions", recovered), "channel_id", channelID) }()
	key := b.teamID + "/" + b.botUserID + "/" + channelID
	saved := b.recovery.Boundary(key)
	firstStart := saved.IsZero()
	if saved.IsZero() {
		saved = b.startup
		if saved.IsZero() {
			saved = now
		}
		if err := b.recovery.Initialize(key, saved); err != nil {
			return err
		}
		b.logger.Info("first start recovery skipped", "channel_id", channelID, "before", saved)
	}
	start := saved.Add(-recoveryOverlap)
	if firstStart {
		start = saved
	}
	if origin := b.recovery.Started(key); !origin.IsZero() && start.Before(origin) {
		start = origin
	}
	floor := now.Add(-recoveryLookback)
	if start.Before(floor) {
		b.logger.Warn("recovery range not fetched", "channel_id", channelID, "range_start", saved, "range_end", floor)
		start = floor
	}
	scannedRoots := make(map[string]bool)
	scanThread := func(root string, from time.Time) error {
		if scannedRoots[root] {
			return nil
		}
		scannedRoots[root] = true
		cursor := ""
		for {
			if err := b.waitRecoveryPace(ctx); err != nil {
				return err
			}
			messages, more, next, err := b.api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: root, Cursor: cursor, Inclusive: true, Oldest: root, Latest: slackTime(now), Limit: 200})
			if err != nil {
				return fmt.Errorf("conversations.replies root %s: %w", root, err)
			}
			for _, msg := range messages {
				if msg.Timestamp == root || compareSlackTS(msg.Timestamp, slackTime(from)) < 0 || compareSlackTS(msg.Timestamp, slackTime(now)) > 0 {
					continue
				}
				delivered, err := b.recoverMessage(channelID, msg, root)
				if err != nil {
					return err
				}
				recovered += delivered
			}
			if !more {
				break
			}
			if next == "" {
				return fmt.Errorf("conversations.replies root %s: missing next cursor", root)
			}
			cursor = next
		}
		return nil
	}
	cursor := ""
	for {
		if err := b.waitRecoveryPace(ctx); err != nil {
			return err
		}
		page, err := b.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channelID, Cursor: cursor, Oldest: slackTime(start), Latest: slackTime(now), Inclusive: true, Limit: 200})
		if err != nil {
			b.logRecoveryFailure(channelID, start, now, err)
			return err
		}
		for _, msg := range page.Messages {
			if compareSlackTS(msg.Timestamp, slackTime(start)) < 0 || compareSlackTS(msg.Timestamp, slackTime(now)) > 0 {
				continue
			}
			delivered, err := b.recoverMessage(channelID, msg, "")
			if err != nil {
				b.logRecoveryFailure(channelID, start, now, err)
				return err
			}
			recovered += delivered
			if msg.ReplyCount > 0 {
				if err := scanThread(msg.Timestamp, start); err != nil {
					b.logRecoveryFailure(channelID, start, now, err)
					return err
				}
			}
		}
		if !page.HasMore {
			break
		}
		if page.ResponseMetaData.NextCursor == "" {
			err := fmt.Errorf("conversations.history: missing next cursor")
			b.logRecoveryFailure(channelID, start, now, err)
			return err
		}
		cursor = page.ResponseMetaData.NextCursor
	}
	if subs != nil {
		roots := subs.ThreadRoots(channelID)
		for _, root := range roots {
			threadSaved := b.recovery.ThreadBoundary(key, root)
			if threadSaved.IsZero() {
				threadSaved = saved
				if err := b.recovery.SetThreadBoundary(key, root, threadSaved); err != nil {
					return err
				}
			}
			threadStart := threadSaved.Add(-recoveryOverlap)
			if origin := b.recovery.Started(key); !origin.IsZero() && threadStart.Before(origin) {
				threadStart = origin
			}
			if threadStart.Before(floor) {
				b.logger.Warn("recovery thread range not fetched", "channel_id", channelID, "thread_ts", root, "range_start", threadSaved, "range_end", floor)
				threadStart = floor
			}
			if err := scanThread(root, threadStart); err != nil {
				b.logger.Warn("recovery thread scan failed", "channel_id", channelID, "thread_ts", root, "range_start", threadStart, "range_end", now, "error", err)
				continue
			}
			if err := b.recovery.SetThreadBoundary(key, root, now); err != nil {
				return err
			}
		}
		if err := b.recovery.RetainThreads(key, roots); err != nil {
			return err
		}
	}
	if err := b.recovery.SetBoundary(key, now); err != nil {
		b.logRecoveryFailure(channelID, start, now, err)
		return err
	}
	b.logger.Info("recovery scan complete", "channel_id", channelID, "range_start", start, "range_end", now)
	return nil
}

func (b *BotAdapter) logRecoveryFailure(channelID string, start, end time.Time, err error) {
	b.logger.Warn("recovery scan failed", "channel_id", channelID, "range_start", start, "range_end", end, "error", err)
}
func (b *BotAdapter) recoverMessage(channelID string, msg slack.Message, root string) (int, error) {
	if !renderableMessage(msg) || msg.BotID != "" || msg.User == "" || !strings.Contains(msg.Text, "<@"+b.botUserID+">") {
		return 0, nil
	}
	if root == "" {
		root = msg.Timestamp
	}
	ok, delivered := b.routeMention(&slackevents.AppMentionEvent{User: msg.User, Channel: channelID, TimeStamp: msg.Timestamp, ThreadTimeStamp: root, Text: msg.Text})
	if !ok {
		return 0, fmt.Errorf("route recovered mention %s failed", msg.Timestamp)
	}
	if delivered > 0 {
		return 1, nil
	}
	return 0, nil
}
func (b *BotAdapter) waitRecoveryPace(ctx context.Context) error {
	if b.recoveryPace == 0 {
		return nil
	}
	timer := time.NewTimer(b.recoveryPace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
