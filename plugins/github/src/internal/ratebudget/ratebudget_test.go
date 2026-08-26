package ratebudget

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/flocktest"
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
	// A frozen, second-aligned clock makes the expected wait exact: real time
	// never elapses between RecordThrottle and Wait, so there is no tolerance
	// window for a loaded CI runner to blow through.
	now := time.Now().Truncate(time.Second)
	g.now = func() time.Time { return now }
	if err := g.RecordThrottle(0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != minBackoff {
		t.Errorf("wait = %v, want exactly %v (exponential fallback, first throttle)", wait, minBackoff)
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
	now := time.Now().Truncate(time.Second)
	g.now = func() time.Time { return now }
	if err := g.RecordThrottle(5*time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != 5*time.Minute {
		t.Errorf("wait = %v, want exactly 5m (Retry-After honored)", wait)
	}
}

// A future X-RateLimit-Reset is honored when no Retry-After is given.
func TestGuard_RateLimitResetHonored(t *testing.T) {
	g := NewGuard(t.TempDir())
	now := time.Now().Truncate(time.Second)
	g.now = func() time.Time { return now }
	reset := now.Add(10 * time.Minute)
	if err := g.RecordThrottle(0, reset); err != nil {
		t.Fatal(err)
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != 10*time.Minute {
		t.Errorf("wait = %v, want exactly 10m (rate-limit reset honored)", wait)
	}
}

// Repeated throttles with no header guidance back off exponentially, capped.
func TestGuard_ExponentialFallbackCapped(t *testing.T) {
	g := NewGuard(t.TempDir())
	now := time.Now().Truncate(time.Second)
	g.now = func() time.Time { return now }
	for range 20 {
		if err := g.RecordThrottle(0, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	wait, err := g.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait != maxBackoff {
		t.Errorf("wait = %v, want capped at exactly %v after many throttles", wait, maxBackoff)
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
	now := time.Now().Truncate(time.Second)
	g.now = func() time.Time { return now }
	// A short Retry-After so the window elapses within the test — RecordSuccess
	// must not itself clear a still-pending backoff (a success observed by one
	// caller doesn't prove every other caller sharing the budget is clear
	// too), so the counter reset can only be observed once time has actually
	// passed. The clock is advanced explicitly rather than slept through, so
	// the elapsed time is exact instead of "at least".
	for range 5 {
		if err := g.RecordThrottle(time.Second, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Second)
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
	if wait != minBackoff {
		t.Errorf("wait = %v, want exactly %v (counter reset by success)", wait, minBackoff)
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

// The Linux NFS client rejects LOCK_EX on an O_RDONLY descriptor with EBADF,
// even though local filesystems tolerate it. This test inspects the lock
// file descriptor's own open flags via /proc, so it catches the regression
// even on a local (non-NFS) test filesystem.
func TestGuard_UpdateOpensLockFileWritable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lock fd flags are inspected via /proc, which is Linux-specific")
	}

	g := NewGuard(t.TempDir())
	lockPath := g.path + ".lock"

	err := g.update(func(*state) error {
		accMode, err := flocktest.AccessMode(lockPath)
		if err != nil {
			return err
		}
		if accMode == os.O_RDONLY {
			return fmt.Errorf("lock file opened O_RDONLY; exclusive lock (LOCK_EX) requires a writable descriptor on NFS")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}
