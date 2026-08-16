package watcher

import (
	"encoding/json"
	"os/exec"
)

// Sweep drops every subscription whose owning session no longer exists.
//
// Session non-existence can only be learned by asking plect directly — a
// "down" session (pane stopped, `plect up` can resume it later) is not
// destroyed and must not be swept, so this needs plect's own session list,
// not just a local heuristic. That's also why this runs once at startup
// rather than every Tick: Tick's own gh-polling loop stays decoupled from
// probing plect state (see TestPoller_DoesNotPruneSubscriptionsDuringPoll),
// and a destroyed session's subscriptions get an immediate, explicit
// unsubscribe from the provider's cleanup hook anyway — this sweep is the
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

// liveSessions returns every session name plect currently knows about, in
// any run state (up, down, ...) — everything short of destroyed.
func (p *Poller) liveSessions() (map[string]bool, error) {
	out, err := exec.Command(p.plect(), "ls", "--json").Output()
	if err != nil {
		return nil, err
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
