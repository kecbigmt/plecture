package adapter

import (
	"os"
	"os/exec"
	"sort"
)

type publishedEvent struct {
	SessionName string
	Type        string
	Source      string
	Direction   string
	Summary     string
	Body        string
	Meta        map[string]string
}

type eventPublisher interface {
	PublishSessionEvent(sessionName string, ev publishedEvent) error
}

type cliEventPublisher struct{}

// Sorting metadata makes CLI invocations stable across map iteration order.
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

func (cliEventPublisher) PublishSessionEvent(sessionName string, ev publishedEvent) error {
	cmd := exec.Command("plect", captureArgs(sessionName, ev.Type, ev.Source, ev.Direction, ev.Summary, ev.Body, ev.Meta)...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// publishEvent records one event in plect's session event log.
func (a *Adapter) publishEvent(sessionName, eventType, source, direction, summary, body string, meta map[string]string) error {
	if sessionName == "" {
		return nil
	}
	publisher := a.eventPublisher
	if publisher == nil {
		publisher = cliEventPublisher{}
	}
	if err := publisher.PublishSessionEvent(sessionName, publishedEvent{
		Type:      eventType,
		Source:    source,
		Direction: direction,
		Summary:   summary,
		Body:      body,
		Meta:      meta,
	}); err != nil {
		a.logger.Warn("plect event publish failed",
			"component", "slack-adapter", "event", "publish_failed",
			"session_name", sessionName, "type", eventType, "error", err)
		return err
	}
	return nil
}

func (a *Adapter) publishInboundReply(sub Subscriber, user, userID, text string) error {
	return a.publishEvent(sub.SessionName, "slack.message", "slack", "inbound", user+" via Slack", text,
		map[string]string{"user": user, "user_id": userID, "thread_ts": sub.ThreadTS, "channel_id": sub.ChannelID})
}
