package ratebudget

import (
	"os"
	"testing"
	"time"
)

func TestGuard_ClearByDefault(t *testing.T) {
	g := NewGuard(t.TempDir())
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != 0 {
		t.Errorf("wait = %v, want 0 (no throttle recorded)", wait)
	}
}

// A 403/429 must impose a shared backoff a second caller (a different
// process in production, this same Guard in the test) observes immediately —
// the mechanism requires: no immediate retry within the window.
func TestGuard_ThrottleBlocksSubsequentCalls(t *testing.T) {
	g := NewGuard(t.TempDir())
	if err := g.RecordThrottle(0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < minBackoff-time.Second || wait > minBackoff {
		t.Errorf("wait = %v, want ~%v (exponential fallback, first throttle)", wait, minBackoff)
	}
}

// The header-less fallback floor must be at least one minute: a caller with
// no header access at all (the --paginate passthrough path) is the one
// place a throttle can only ever use this fallback, and "no immediate retry
// within one minute of the same endpoint" is the guarantee callers rely on.
func TestGuard_HeaderlessFallbackFloorIsAtLeastOneMinute(t *testing.T) {
	if minBackoff < time.Minute {
		t.Fatalf("minBackoff = %v, want >= 1m", minBackoff)
	}
}

// A Retry-After header takes precedence over the exponential fallback.
func TestGuard_RetryAfterHonored(t *testing.T) {
	g := NewGuard(t.TempDir())
	if err := g.RecordThrottle(5*time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < 4*time.Minute+55*time.Second || wait > 5*time.Minute {
		t.Errorf("wait = %v, want ~5m (Retry-After honored)", wait)
	}
}

// A future X-RateLimit-Reset is honored when no Retry-After is given.
func TestGuard_RateLimitResetHonored(t *testing.T) {
	g := NewGuard(t.TempDir())
	reset := time.Now().Add(10 * time.Minute)
	if err := g.RecordThrottle(0, reset); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < 9*time.Minute+55*time.Second || wait > 10*time.Minute {
		t.Errorf("wait = %v, want ~10m (rate-limit reset honored)", wait)
	}
}

// Repeated throttles with no header guidance back off exponentially, capped.
func TestGuard_ExponentialFallbackCapped(t *testing.T) {
	g := NewGuard(t.TempDir())
	for range 20 {
		if err := g.RecordThrottle(0, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait > maxBackoff || wait < maxBackoff-5*time.Second {
		t.Errorf("wait = %v, want capped at ~%v after many throttles", wait, maxBackoff)
	}
}

// A second, later throttle must never shorten an already-longer pending
// backoff (e.g. a Retry-After-driven window still running when another 403
// with no header info arrives).
func TestGuard_ThrottleNeverShortensPendingBackoff(t *testing.T) {
	g := NewGuard(t.TempDir())
	if err := g.RecordThrottle(10*time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := g.RecordThrottle(0, time.Time{}); err != nil { // exponential fallback: ~30s
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < 9*time.Minute {
		t.Errorf("wait = %v, a shorter throttle must not shorten the pending 10m backoff", wait)
	}
}

// RecordSuccess resets the exponential counter so the next throttle (after
// GitHub recovers) starts from the minimum window again, not the capped one.
func TestGuard_SuccessResetsConsecutiveCount(t *testing.T) {
	g := NewGuard(t.TempDir())
	// A short Retry-After so the window elapses for real within the test —
	// RecordSuccess must not itself clear a still-pending backoff (a success
	// observed by one caller doesn't prove every other caller sharing the
	// budget is clear too), so the counter reset can only be observed once
	// time has actually passed.
	for range 5 {
		if err := g.RecordThrottle(50*time.Millisecond, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if err := g.RecordSuccess(); err != nil {
		t.Fatal(err)
	}
	if err := g.RecordThrottle(0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait > minBackoff || wait < minBackoff-time.Second {
		t.Errorf("wait = %v, want ~%v (counter reset by success)", wait, minBackoff)
	}
}

// RecordSuccess must NOT clear a still-pending backoff: a success observed by
// one caller doesn't prove every other caller sharing the budget is clear —
// this is what keeps the budget "single" under concurrent callers.
func TestGuard_SuccessDoesNotClearPendingBackoff(t *testing.T) {
	g := NewGuard(t.TempDir())
	if err := g.RecordThrottle(10*time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := g.RecordSuccess(); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < 9*time.Minute {
		t.Errorf("wait = %v, RecordSuccess must not clear a pending backoff", wait)
	}
}

// Two Guards over the same directory share state — this is what makes the
// budget "single" across processes (the poll daemon and one-shot gh-api
// invocations each construct their own Guard against the same data dir).
func TestGuard_SharedAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	a := NewGuard(dir)
	b := NewGuard(dir)
	if err := a.RecordThrottle(2*time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := b.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < time.Minute {
		t.Errorf("second Guard instance over the same dir = %v, want to observe the first's throttle", wait)
	}
}

func TestGuard_CorruptStateDegradesToClear(t *testing.T) {
	dir := t.TempDir()
	g := NewGuard(dir)
	if err := g.RecordThrottle(time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the file directly.
	if err := os.WriteFile(g.path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != 0 {
		t.Errorf("wait = %v, want 0 (corrupt state must not wedge every caller)", wait)
	}
}
