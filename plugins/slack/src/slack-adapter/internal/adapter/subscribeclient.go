package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// MentionFilter is the per-population policy `subscribe unbound-mentions`
// applies to the raw feed. The resident adapter stays policy-free so one
// connection serves any number of populations, each with its own filter.
type MentionFilter struct {
	// ChannelIDs matches every channel when empty.
	ChannelIDs []string
	// UserIDs matches every user when empty; otherwise it is compared
	// exactly against the mentioning user's Slack ID.
	UserIDs []string
	// DeniedUserMessage is posted into the thread of a mention rejected by
	// UserIDs; empty means such mentions are dropped silently.
	DeniedUserMessage string
	// DeniedChannelMessage is posted into the thread of a mention outside
	// ChannelIDs; empty means such mentions are dropped silently.
	DeniedChannelMessage string
}

type mentionVerdict int

const (
	verdictEmit mentionVerdict = iota
	verdictDrop
	verdictDenyUser
	verdictDenyChannel
)

// judge checks the channel before the user so a mention in an unwatched
// channel is answered with the channel message only, never the user one.
func (f MentionFilter) judge(item unboundMentionItem) mentionVerdict {
	if !listAllows(f.ChannelIDs, item.ChannelID) {
		if f.DeniedChannelMessage == "" {
			return verdictDrop
		}
		return verdictDenyChannel
	}
	if listAllows(f.UserIDs, item.UserID) {
		return verdictEmit
	}
	if f.DeniedUserMessage == "" {
		return verdictDrop
	}
	return verdictDenyUser
}

type denyKey struct{ channelID, threadTS, userID string }

// RunSubscribeUnboundMentions connects to the resident adapter's
// /unbound-mentions feed at baseURL and writes one query.subscribe item per
// line to out for each mention that filter lets through.
//
// It returns nil only when ctx itself ends the connection — the process was
// asked to stop. Any other termination (the initial connect failing, the
// resident restarting mid-stream, or the stream otherwise ending) is a
// source failure and returns a non-nil error: a caller cannot tell a quiet
// channel from a dead connection any other way, and mistaking the latter
// for the former would mean missing every mention until something else
// notices.
func RunSubscribeUnboundMentions(ctx context.Context, baseURL string, filter MentionFilter, out io.Writer) error {
	base := strings.TrimRight(baseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/unbound-mentions", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("connect to %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unbound-mentions stream returned %s", resp.Status)
	}

	denied := make(map[denyKey]struct{})
	dec := json.NewDecoder(resp.Body)
	for {
		var item unboundMentionItem
		if err := dec.Decode(&item); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("unbound-mentions stream ended: %w", err)
		}
		verdict := filter.judge(item)
		if verdict == verdictDrop {
			continue
		}
		if verdict != verdictEmit {
			text := filter.DeniedUserMessage
			if verdict == verdictDenyChannel {
				text = filter.DeniedChannelMessage
			}
			key := denyKey{item.ChannelID, item.ThreadTS, item.UserID}
			if _, done := denied[key]; done {
				continue
			}
			// Recorded before posting and never retried: the bot may not be a
			// member of the channel, where every attempt fails the same way,
			// and a failed courtesy reply must not end the subscription over
			// every allowed user's mentions either.
			denied[key] = struct{}{}
			if err := postDenyReply(ctx, base, item, text); err != nil {
				slog.Warn("deny reply not posted", "channel_id", item.ChannelID, "thread_ts", item.ThreadTS, "error", err)
			}
			continue
		}
		line, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("encode item: %w", err)
		}
		if _, err := out.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write item: %w", err)
		}
	}
}

// postDenyReply goes through the resident adapter's POST /messages because
// only the resident holds Slack credentials.
func postDenyReply(ctx context.Context, base string, item unboundMentionItem, text string) error {
	body, err := json.Marshal(postMessageRequest{ChannelID: item.ChannelID, ThreadTS: item.ThreadTS, Text: text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /messages returned %s", resp.Status)
	}
	return nil
}

// listAllows treats an empty list as "no restriction".
func listAllows(allowed []string, id string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == id {
			return true
		}
	}
	return false
}
