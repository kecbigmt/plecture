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

// dispatchUnboundMention only ever inspects the command's exit status
// (logged, never retried): which workflow to start and which channels to
// honour is deployment policy this plugin must not encode.
func (a *Adapter) dispatchUnboundMention(ev *slackevents.AppMentionEvent, threadTS string) {
	if a.cfg.OnUnboundMention == "" {
		return
	}

	resolver := a.permalinkResolver
	if resolver == nil {
		resolver = a
	}
	link, err := resolver.permalink(ev.Channel, threadTS)
	if err != nil {
		a.logger.Warn("on_unbound_mention skipped: failed to resolve permalink",
			"thread_ts", threadTS, "channel_id", ev.Channel, "error", err)
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
