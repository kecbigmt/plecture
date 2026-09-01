package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// unboundMentionPayload is the JSON document handed to on_unbound_mention on
// stdin. Field names are the deployment-facing contract documented in the
// plugin's README; keep them stable.
type unboundMentionPayload struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	TS        string `json:"ts"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Permalink string `json:"permalink"`
}

// permalinkResolver looks up the canonical Slack permalink for an existing
// message. A separate seam from threadFetcher because it is needed only by
// the on_unbound_mention path, not by deliberation transcript building.
type permalinkResolver interface {
	permalink(channelID, ts string) (string, error)
}

func (a *Adapter) permalink(channelID, ts string) (string, error) {
	return a.api.GetPermalink(&slack.PermalinkParameters{Channel: channelID, Ts: ts})
}

// mentionHookRunner executes the deployment-configured on_unbound_mention
// command. The default implementation shells out (no import of plect/app),
// matching how this adapter already records session events via the plect
// CLI (capture.go).
type mentionHookRunner interface {
	Run(command string, payload []byte) error
}

type cliMentionHookRunner struct{}

func (cliMentionHookRunner) Run(command string, payload []byte) error {
	cmd := exec.Command(command)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dispatchUnboundMention runs on_unbound_mention for a mention that resolved
// to no subscription. It fires once and only ever inspects the command's exit
// status (logged, never retried) — everything else, such as which workflow to
// start or which channels to honour, is the command's job: core/the plugin
// must not encode that deployment policy.
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
