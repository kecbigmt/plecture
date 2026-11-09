package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var threadResourcePattern = regexp.MustCompile(`^/archives/([A-Z0-9]+)/p(\d{16})$`)
var mentionResourcePattern = regexp.MustCompile(`^[A-Z0-9]+$`)

// ParseMentionResource reads slack://<team_id>/<bot_user_id>/mentions. The
// bot user id is part of the identity because several Apps installed in one
// workspace share its team id, each with its own adapter and subscribers.
func ParseMentionResource(resource string) (teamID, botUserID string, err error) {
	u, err := url.Parse(resource)
	if err != nil || u.Scheme != "slack" || !mentionResourcePattern.MatchString(u.Host) || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", errors.New("invalid Slack mention resource")
	}
	bot, ok := strings.CutSuffix(strings.TrimPrefix(u.Path, "/"), "/mentions")
	if !ok || !mentionResourcePattern.MatchString(bot) {
		return "", "", errors.New("invalid Slack mention resource")
	}
	return u.Host, bot, nil
}

func ParseThreadResource(resource string) (channelID, threadTS string, err error) {
	u, err := url.Parse(resource)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".slack.com") || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return "", "", errors.New("invalid Slack thread resource")
	}
	parts := threadResourcePattern.FindStringSubmatch(u.Path)
	if parts == nil {
		return "", "", errors.New("invalid Slack thread permalink")
	}
	ts := parts[2][:10] + "." + parts[2][10:]
	query := u.Query()
	queryTS := query.Get("thread_ts")
	if u.RawQuery != "" && (len(query) != 2 || len(query["thread_ts"]) != 1 || len(query["cid"]) != 1 || queryTS == "") {
		return "", "", errors.New("invalid Slack thread permalink query")
	}
	if queryTS != "" {
		if !isSlackTimestampShape(queryTS) || u.Query().Get("cid") != parts[1] {
			return "", "", errors.New("thread permalink identity mismatch")
		}
	}
	if queryTS != "" {
		ts = queryTS
	}
	return parts[1], ts, nil
}

func ThreadSubscription(ctx context.Context, client *http.Client, baseURL, action, session, resource, through string) error {
	channel, thread, err := ParseThreadResource(resource)
	if err != nil {
		return err
	}
	if session == "" {
		return errors.New("session is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	var req *http.Request
	switch action {
	case "subscribe":
		body, _ := json.Marshal(subscribeRequest{SessionName: session, ChannelID: channel, ThreadTS: thread, CatchUpThrough: through})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/subscribe", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	case "unsubscribe":
		values := url.Values{"session_name": {session}, "thread_ts": {thread}}
		req, err = http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/subscribe?"+values.Encode(), nil)
	default:
		return fmt.Errorf("unknown thread subscription action %q", action)
	}
	if err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("slack-adapter %s: %s: %s", action, response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func MentionSubscription(ctx context.Context, client *http.Client, baseURL, action, session, resource string) error {
	team, bot, err := ParseMentionResource(resource)
	if err != nil {
		return err
	}
	if session == "" {
		return errors.New("session is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	var req *http.Request
	switch action {
	case "subscribe":
		body, _ := json.Marshal(subscribeRequest{SessionName: session, ThreadTS: mentionSubscriptionKey, TeamID: team, BotUserID: bot})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/subscribe", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	case "unsubscribe":
		values := url.Values{"session_name": {session}, "thread_ts": {mentionSubscriptionKey}, "bot_user_id": {bot}}
		req, err = http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/subscribe?"+values.Encode(), nil)
	default:
		return fmt.Errorf("unknown mention subscription action %q", action)
	}
	if err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("slack-adapter %s: %s: %s", action, response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (a *Adapter) SweepSubscriptions(ctx context.Context, liveSessions func(context.Context) (map[string]bool, error)) {
	subs := a.broker.List()
	if len(subs) == 0 {
		return
	}
	live, err := liveSessions(ctx)
	if err != nil {
		a.logger.Warn("subscriber sweep skipped", "error", err)
		return
	}
	for _, sub := range subs {
		if !live[sub.SessionName] {
			a.broker.Unsubscribe(sub.SessionName, sub.ThreadTS)
		}
	}
}

func LiveSessions(ctx context.Context) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "plect", "ls", "--json")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("plect ls --json: %w", err)
	}
	var rows []struct {
		SessionName string `json:"session_name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(rows))
	for _, row := range rows {
		live[row.SessionName] = true
	}
	return live, nil
}
