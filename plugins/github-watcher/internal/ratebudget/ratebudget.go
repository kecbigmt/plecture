// Package ratebudget implements a single, cross-process call budget: every
// caller sharing one on-disk backoff state file consults it before issuing a
// request against whatever rate-limited API it fronts. A 429/403-style
// throttle response sets a shared backoff window; any caller — in this
// process or another — that checks the guard before that window elapses is
// told to wait instead of retrying immediately. The mechanism itself carries
// no knowledge of which API it's protecting; callers own that (github-watcher
// uses it to front the GitHub REST API).
package ratebudget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kecbigmt/plect/contracts/atomicfile"
)

// minBackoff/maxBackoff bound the exponential fallback used when a 403/429
// response carries neither Retry-After nor a not-yet-elapsed X-RateLimit-Reset
// (the --paginate passthrough path detects a throttle from gh's plain-text
// error only, with no header access at all). minBackoff must stay >= 1
// minute: "no immediate retry within one minute of the same endpoint" is the
// guarantee callers rely on, so a header-less throttle's floor can't be
// shorter than that — a header-bearing throttle may still set a longer wait
// when GitHub says so.
const (
	minBackoff = 60 * time.Second
	maxBackoff = 15 * time.Minute
)

// Guard is the on-disk shared backoff state. All methods lock the file for
// the duration of the read-modify-write, so concurrent processes sharing the
// same dir never race.
type Guard struct {
	path string
}

// state is the on-disk shape.
type state struct {
	// BackoffUntil is a Unix-seconds timestamp; zero (or past) means clear.
	BackoffUntil int64 `json:"backoff_until,omitempty"`
	// Consecutive counts unbroken throttle responses since the last success,
	// driving the exponential fallback when the caller has no precise window.
	Consecutive int `json:"consecutive_throttles,omitempty"`
}

// NewGuard creates a Guard rooted at dir. Callers with several independent
// process entry points sharing one budget (a daemon plus one-shot CLI
// invocations) must point them all at the same dir explicitly — there is no
// implicit default location, since this package has no opinion on which
// caller or API owns that budget.
func NewGuard(dir string) *Guard {
	return &Guard{path: filepath.Join(dir, "rate-budget.json")}
}

// Wait reports how long the caller must wait before it may call the
// rate-limited API (zero means clear to proceed now). It does not itself
// sleep or retry — the caller decides whether to skip this cycle or block,
// and must not make the call while the returned duration is positive.
func (g *Guard) Wait() (time.Duration, error) {
	var remaining time.Duration
	err := g.update(func(s *state) error {
		remaining = g.remaining(s)
		return nil
	})
	return remaining, err
}

func (g *Guard) remaining(s *state) time.Duration {
	if s.BackoffUntil == 0 {
		return 0
	}
	remaining := time.Until(time.Unix(s.BackoffUntil, 0))
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// RecordThrottle registers a 403/429 response and extends the shared backoff
// window. retryAfter (from a Retry-After header) takes precedence when
// positive; otherwise a future rateLimitReset (X-RateLimit-Reset) is used;
// otherwise an exponential fallback (minBackoff * 2^consecutive, capped at
// maxBackoff) guarantees a window even when gh's error text carries neither
// header.
func (g *Guard) RecordThrottle(retryAfter time.Duration, rateLimitReset time.Time) error {
	return g.update(func(s *state) error {
		now := time.Now()
		var until time.Time
		switch {
		case retryAfter > 0:
			until = now.Add(retryAfter)
		case !rateLimitReset.IsZero() && rateLimitReset.After(now):
			until = rateLimitReset
		default:
			// Shift is capped well below where minBackoff<<shift could
			// overflow int64 nanoseconds; maxBackoff already caps the
			// result long before that ceiling matters.
			shift := min(s.Consecutive, 10)
			delay := min(minBackoff<<shift, maxBackoff)
			until = now.Add(delay)
		}
		// A fresh throttle never shortens an already-longer pending backoff
		// (e.g. a second 403 arriving while a Retry-After-driven window from
		// the first is still running).
		if current := time.Unix(s.BackoffUntil, 0); s.BackoffUntil != 0 && current.After(until) {
			until = current
		}
		s.BackoffUntil = until.Unix()
		if s.Consecutive < 30 { // bound the shift in the exponential fallback above
			s.Consecutive++
		}
		return nil
	})
}

// RecordSuccess clears the consecutive-throttle counter after a good
// response, so the next throttle (once GitHub actually starts allowing calls
// again) starts from the minimum window rather than a stacked-up exponential
// one. It deliberately does NOT clear BackoffUntil: a success observed by
// this caller doesn't prove every other caller sharing the budget is clear
// too, and a concurrent RecordThrottle could be racing this same call — only
// time elapsing (or an explicit Wait() check before the next call) may lift
// an active shared backoff.
func (g *Guard) RecordSuccess() error {
	return g.update(func(s *state) error {
		s.Consecutive = 0
		return nil
	})
}

func (g *Guard) load() (*state, error) {
	s := &state{}
	data, err := os.ReadFile(g.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, s); err != nil {
		// A corrupt state file degrades to "no backoff known" rather than
		// blocking every caller forever.
		return &state{}, nil
	}
	return s, nil
}

// update is the locked read-modify-write primitive, mirroring
// github-watcher's Store.update.
func (g *Guard) update(fn func(*state) error) error {
	dir := filepath.Dir(g.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(g.path+".lock", os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	s, err := g.load()
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(g.path, data)
}
