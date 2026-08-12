package adapter

import (
	"os"
	"os/exec"
	"sort"

	protocol "github.com/plecture/plect/contracts/channel-protocol"
)

// Conversation capture: slack-adapter mirrors the Slack↔Claude traffic that
// flows through it into plect's per-session event log, so a session's timeline
// shows the actual back-and-forth, not just GitHub/lifecycle events.
//
// Both directions pass through this adapter (inbound Slack messages here;
// Claude replies / permission prompts via the channel-server socket callbacks),
// and the adapter already knows the plect session_name via its broker — so all
// capture lives here and channel-server stays source-independent (it must not
// know about plect sessions). Recording is done by exec'ing the `plect` CLI (no
// import of plect/app) and is best-effort: a failure is logged, never blocking
// delivery.

// captureArgs builds the `plect event publish` argument vector. Captured event
// types are intentionally outside workflow channel includes so Slack/Claude
// traffic is recorded without echoing back out.
// Meta keys are sorted for deterministic output (and tests).
func captureArgs(sessionName, eventType, source, direction, summary, body string, meta map[string]string) []string {
	args := []string{
		"event", "publish", sessionName,
		"--type", eventType,
		"--source", source,
		"--direction", direction,
	}
	if summary != "" {
		args = append(args, "--summary", summary)
	}
	if body != "" {
		args = append(args, "--body", body)
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if meta[k] != "" {
			args = append(args, "--meta", k+"="+meta[k])
		}
	}
	return args
}

// publishEvent records one conversation event, best-effort.
func (a *Adapter) publishEvent(sessionName, eventType, source, direction, summary, body string, meta map[string]string) {
	if sessionName == "" {
		return // no session context to key the log on
	}
	cmd := exec.Command("plect", captureArgs(sessionName, eventType, source, direction, summary, body, meta)...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		a.logger.Warn("plect event capture failed",
			"component", "slack-adapter", "event", "capture_failed",
			"session_name", sessionName, "type", eventType, "error", err)
	}
}

// captureInbound records a Slack message delivered to a session.
func (a *Adapter) captureInbound(sub Subscriber, msg protocol.MessagePayload) {
	a.publishEvent(sub.SessionName, "slack.message", "slack", "inbound",
		msg.User+" via Slack", msg.Text,
		map[string]string{
			"user":       msg.User,
			"user_id":    msg.UserID,
			"thread_ts":  msg.ThreadTS,
			"channel_id": sub.ChannelID,
		})
}

// captureOutbound records a message channel-server sent back to Slack — a Claude
// reply or a permission prompt. The session is resolved from the thread (the
// socket callbacks carry only thread_ts).
func (a *Adapter) captureOutbound(threadTS, eventType, body string) {
	sub, ok := a.broker.Find(threadTS)
	if !ok {
		return
	}
	summary := "Claude reply"
	if eventType == "claude.permission_request" {
		summary = "Claude permission request"
	}
	a.publishEvent(sub.SessionName, eventType, "claude", "outbound", summary, body,
		map[string]string{"thread_ts": threadTS, "channel_id": sub.ChannelID})
}
