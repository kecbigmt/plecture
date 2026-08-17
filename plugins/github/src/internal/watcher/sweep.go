package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/procexec"
)

// defaultPlectListTimeout bounds Sweep's `plect ls --json` call. `plect ls`
// fans out a healthcheck per session — one hung healthcheck (a movement
// source script that never returns) must not block the watcher from ever
// reaching its first Tick, which is what an unbounded call would risk.
const defaultPlectListTimeout = 15 * time.Second

// Sweep drops every subscription whose owning session no longer exists.
//
// Session non-existence can only be learned by asking plect directly — a
// "down" session (pane stopped, `plect up` can resume it later) is not
// destroyed and must not be swept, so this needs plect's own session list,
// not just a local heuristic. That's also why this runs once at startup
// rather than every Tick: Tick's own gh-polling loop stays decoupled from
// probing plect state (see TestPoller_DoesNotPruneSubscriptionsDuringPoll),
// and a destroyed session's subscriptions get an immediate, explicit
// unsubscribe from the workspace provider's cleanup hook anyway — this sweep is the
// backstop for whatever that path missed (a watcher outage during destroy,
// registry entries predating this fix), not the primary removal path.
func (p *Poller) Sweep() {
	subs, err := p.Store.All()
	if err != nil {
		p.Logger.Error("load subscriptions for sweep", "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	live, err := p.liveSessions()
	if err != nil {
		// Without a trustworthy session list, dropping subscriptions risks
		// discarding live ones on a `plect` hiccup — skipping the sweep is
		// safer than guessing.
		p.Logger.Warn("sweep: list sessions failed; skipping", "error", err)
		return
	}
	for _, sub := range subs {
		if sub == nil || live[sub.SessionName] {
			continue
		}
		if err := p.Store.UnsubscribeResource(sub.SessionName, sub.Resource); err != nil {
			p.Logger.Warn("sweep: drop orphaned subscription failed", "session", sub.SessionName, "resource", sub.Resource, "error", err)
			continue
		}
		p.Logger.Info("sweep: dropped subscription for a destroyed session", "session", sub.SessionName, "resource", sub.Resource)
	}
}

func (p *Poller) plect() string {
	if p.PlectBin != "" {
		return p.PlectBin
	}
	return "plect"
}

func (p *Poller) plectListTimeout() time.Duration {
	if p.PlectListTimeout > 0 {
		return p.PlectListTimeout
	}
	return defaultPlectListTimeout
}

func (p *Poller) runner() procexec.Runner {
	if p.Runner != nil {
		return p.Runner
	}
	return procexec.Default
}

// liveSessions returns every session name plect currently knows about, in
// any run state (up, down, ...) — everything short of destroyed. Bounded by
// plectListTimeout (see defaultPlectListTimeout): a `plect ls` that hangs
// past it is killed and treated as a failure, same as any other error here.
func (p *Poller) liveSessions() (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.plectListTimeout())
	defer cancel()
	out, stderr, err := p.runner().Run(ctx, "", false, p.plect(), "ls", "--json")
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("%s ls --json: %s", p.plect(), msg)
		}
		return nil, fmt.Errorf("%s ls --json: %w", p.plect(), err)
	}
	var rows []struct {
		SessionName string `json:"session_name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(rows))
	for _, r := range rows {
		live[r.SessionName] = true
	}
	return live, nil
}
