package adapter

import (
	"fmt"
	"sync"
)

type QueryRegistration struct {
	ID     string         `json:"id"`
	Inputs map[string]any `json:"inputs"`
}

type Coverage struct {
	mu      sync.RWMutex
	bots    []Bot
	socket  string
	channel map[string]string
}

func NewCoverage(bots []Bot, socket string) *Coverage {
	return &Coverage{bots: bots, socket: socket, channel: make(map[string]string)}
}

func (c *Coverage) Replace(queries []QueryRegistration) error {
	next := make(map[string]string)
	for _, query := range queries {
		path, _ := query.Inputs["socket_path"].(string)
		if path == "" || path != c.socket {
			return fmt.Errorf("population query %q socket_path does not match service socket_path", query.ID)
		}
		channels, ok := query.Inputs["channel_ids"].([]any)
		if !ok || len(channels) == 0 {
			return fmt.Errorf("population query %q requires channel_ids", query.ID)
		}
		for _, raw := range channels {
			channel, ok := raw.(string)
			if !ok || !slackChannelID.MatchString(channel) {
				return fmt.Errorf("population query %q has invalid channel_id %v", query.ID, raw)
			}
			allowed := false
			for _, bot := range c.bots {
				if bot.AllowsChannel(channel) {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("population query %q names unconfigured channel %q", query.ID, channel)
			}
			if prior := next[channel]; prior != "" {
				return fmt.Errorf("population query channel overlap %q between %q and %q", channel, prior, query.ID)
			}
			next[channel] = query.ID
		}
	}
	c.mu.Lock()
	c.channel = next
	c.mu.Unlock()
	return nil
}

func (c *Coverage) Owner(channel string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.channel[channel]
}

func (c *Coverage) Matches(owner string, channels []string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	listed := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if c.channel[channel] != owner || listed[channel] {
			return false
		}
		listed[channel] = true
	}
	for channel, candidate := range c.channel {
		if candidate == owner && !listed[channel] {
			return false
		}
	}
	return true
}
