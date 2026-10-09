package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

type slackSessionClient struct {
	token   string
	baseURL string
	client  *http.Client
}

func newSlackSessionClient(token, baseURL string, client *http.Client) *slackSessionClient {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &slackSessionClient{token: token, baseURL: baseURL, client: client}
}

func (c *slackSessionClient) SetStatus(channelID, threadTS, status string) error {
	return c.post("agents.sessions.setStatus", url.Values{
		"channel_id": {channelID}, "thread_ts": {threadTS}, "status": {status},
	})
}

func (c *slackSessionClient) StopStream(channelID, ts, text, status string) error {
	values := url.Values{"channel": {channelID}, "ts": {ts}, "session_status": {status}}
	if text != "" {
		values.Set("markdown_text", text)
	}
	return c.post("chat.stopStream", values)
}

func (c *slackSessionClient) post(method string, values url.Values) error {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+method, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("slack %s returned HTTP %d", method, resp.StatusCode)
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("slack %s response: %w", method, err)
	}
	if !result.OK {
		if result.Error == "" {
			result.Error = "unknown_error"
		}
		return slack.SlackErrorResponse{Err: result.Error}
	}
	return nil
}
