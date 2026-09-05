package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// unboundMentionPayload's field names are a deployment-facing contract
// documented in the README and must not be renamed casually.
type unboundMentionPayload struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	TS        string `json:"ts"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Permalink string `json:"permalink"`
}

// permalinkResolver is split out from threadFetcher because only the
// on_unbound_mention path needs it, not deliberation transcript building.
type permalinkResolver interface {
	permalink(channelID, ts string) (string, error)
}

func (a *Adapter) permalink(channelID, ts string) (string, error) {
	return a.api.GetPermalink(&slack.PermalinkParameters{Channel: channelID, Ts: ts})
}

type mentionHookRunner interface {
	Run(command string, payload []byte) error
}

// cliMentionHookRunner shells out rather than importing plect/app directly,
// matching how this adapter already records session events via the plect
// CLI instead of a direct import (see capture.go).
type cliMentionHookRunner struct{}

func (cliMentionHookRunner) Run(command string, payload []byte) error {
	cmd := exec.Command(command)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dispatchUnboundMention feeds both delivery paths from one permalink
// resolution: the opaque on_unbound_mention hook (only if configured) and
// the /unbound-mentions stream (only if something is connected to it). The
// two coexist rather than one superseding the other — see
// docs/adr/2026-09-05-standing-session-dispatch.md's subscribe-only
// observer sketch — until the hook's retirement (a separate, later step).
// The hook command's exit status is only ever inspected, never retried:
// which workflow to start and which channels to honour is deployment
// policy this plugin must not encode.
func (a *Adapter) dispatchUnboundMention(ev *slackevents.AppMentionEvent, threadTS string) {
	hasHook := a.cfg.OnUnboundMention != ""
	hasStreamReaders := a.mentions != nil && a.mentions.hasSubscribers()
	if !hasHook && !hasStreamReaders {
		return
	}

	resolver := a.permalinkResolver
	if resolver == nil {
		resolver = a
	}
	link, err := resolver.permalink(ev.Channel, threadTS)
	if err != nil {
		a.logger.Warn("unbound mention dispatch skipped: failed to resolve permalink",
			"thread_ts", threadTS, "channel_id", ev.Channel, "error", err)
		return
	}

	if a.mentions != nil {
		a.mentions.publish(unboundMentionItem{
			Resource:  link,
			ChannelID: ev.Channel,
			ThreadTS:  threadTS,
			MentionTS: ev.TimeStamp,
		})
	}

	if !hasHook {
		return
	}

	payload, err := json.Marshal(unboundMentionPayload{
		ChannelID: ev.Channel,
		ThreadTS:  threadTS,
		TS:        ev.TimeStamp,
		User:      ev.User,
		Text:      ev.Text,
		Permalink: link,
	})
	if err != nil {
		a.logger.Warn("on_unbound_mention skipped: failed to encode payload",
			"thread_ts", threadTS, "channel_id", ev.Channel, "error", err)
		return
	}

	runner := a.mentionHook
	if runner == nil {
		runner = cliMentionHookRunner{}
	}
	if err := runner.Run(a.cfg.OnUnboundMention, payload); err != nil {
		a.logger.Warn("on_unbound_mention command exited with an error",
			"command", a.cfg.OnUnboundMention, "thread_ts", threadTS, "channel_id", ev.Channel, "error", err)
		return
	}
	a.logger.Info("on_unbound_mention command completed",
		"command", a.cfg.OnUnboundMention, "thread_ts", threadTS, "channel_id", ev.Channel)
}
