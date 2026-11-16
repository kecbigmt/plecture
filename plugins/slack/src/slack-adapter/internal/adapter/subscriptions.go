package adapter

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type SubscriptionResource struct {
	Locator string `json:"locator"`
}
type Subscription struct {
	SessionID string               `json:"session_id"`
	Source    string               `json:"source"`
	Resource  SubscriptionResource `json:"resource"`
}

type Subscriptions struct {
	mu   sync.RWMutex
	rows map[string]Subscription
}

func NewSubscriptions() *Subscriptions { return &Subscriptions{rows: make(map[string]Subscription)} }
func (s *Subscriptions) Apply(add, remove []Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range remove {
		delete(s.rows, subscriptionKey(row))
	}
	for _, row := range add {
		s.rows[subscriptionKey(row)] = row
	}
}
func subscriptionKey(row Subscription) string {
	return row.SessionID + "/" + row.Source + "/" + row.Resource.Locator
}

var threadPermalink = regexp.MustCompile(`^/archives/([A-Z0-9]+)/p(\d{16})$`)

func (s *Subscriptions) ThreadRoots(channel string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make(map[string]bool)
	for _, row := range s.rows {
		u, err := url.Parse(row.Resource.Locator)
		if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".slack.com") {
			continue
		}
		match := threadPermalink.FindStringSubmatch(u.Path)
		if match == nil || match[1] != channel {
			continue
		}
		ts := u.Query().Get("thread_ts")
		if ts == "" {
			ts = match[2][:10] + "." + match[2][10:]
		}
		if isSlackTimestampShape(ts) {
			roots[ts] = true
		}
	}
	out := make([]string, 0, len(roots))
	for ts := range roots {
		out = append(out, ts)
	}
	return out
}
