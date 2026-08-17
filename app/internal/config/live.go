package config

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Live holds a *Config re-resolved from disk on a fixed interval, for a
// long-running process (`plect serve`) that would otherwise pin the
// plugin-mount layer to whatever existed at the moment it called Load. Every
// other consumer (a CLI subcommand) is a short-lived process that calls Load
// fresh per invocation, so it never goes stale; a daemon has no equivalent
// per-invocation boundary, so it needs its own refresh point instead.
type Live struct {
	load     func() (*Config, error)
	interval time.Duration
	logger   *slog.Logger
	cur      atomic.Pointer[Config]
}

// NewLive resolves an initial Config via load (failing loudly the same way a
// one-shot Load would) and returns a Live ready to serve it through Get.
// Call Run to keep it refreshed for the life of ctx.
func NewLive(load func() (*Config, error), interval time.Duration) (*Live, error) {
	cfg, err := load()
	if err != nil {
		return nil, err
	}
	lv := &Live{load: load, interval: interval, logger: slog.Default()}
	lv.cur.Store(cfg)
	return lv, nil
}

// Get returns the most recently resolved Config. Safe for concurrent use.
func (lv *Live) Get() *Config {
	return lv.cur.Load()
}

// Run re-resolves the held Config every interval until ctx ends.
func (lv *Live) Run(ctx context.Context) {
	tick := time.NewTicker(lv.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			lv.refresh()
		}
	}
}

// refresh re-resolves the Config and swaps it in, or logs and keeps the
// previous Config on failure: a transient catalog/lock read error must not
// take an otherwise-healthy daemon down, and the previous Config is still
// valid until proven otherwise.
func (lv *Live) refresh() {
	cfg, err := lv.load()
	if err != nil {
		lv.logger.Warn("config: periodic refresh failed; keeping previous config", "error", err)
		return
	}
	lv.cur.Store(cfg)
}
