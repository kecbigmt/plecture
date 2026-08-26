package apptoken

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/flocktest"
)

func TestCache_MintsWhenEmpty(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "cache.json"))
	var mints int32

	token, err := c.Get(5*time.Minute, func() (string, time.Time, error) {
		atomic.AddInt32(&mints, 1)
		return "minted-token", time.Now().Add(time.Hour), nil
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if token != "minted-token" {
		t.Errorf("token = %q, want minted-token", token)
	}
	if mints != 1 {
		t.Errorf("mints = %d, want 1", mints)
	}
}

func TestCache_ReusesUnexpiredToken(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "cache.json"))
	now := time.Now()
	c.now = func() time.Time { return now }
	var mints int32
	mint := func() (string, time.Time, error) {
		atomic.AddInt32(&mints, 1)
		return "minted-token", now.Add(time.Hour), nil
	}

	if _, err := c.Get(5*time.Minute, mint); err != nil {
		t.Fatal(err)
	}
	token, err := c.Get(5*time.Minute, mint)
	if err != nil {
		t.Fatal(err)
	}
	if token != "minted-token" {
		t.Errorf("token = %q, want the cached token reused", token)
	}
	if mints != 1 {
		t.Errorf("mints = %d, want 1 (second Get must reuse the cache)", mints)
	}
}

// The skew window is the acceptance criterion's "within the skew window of
// expiry" case: a token that still has time left, but less than the caller's
// skew, must be treated as due for refresh rather than handed out to a
// caller who would then have it expire mid-use.
func TestCache_RefreshesWithinSkewWindow(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "cache.json"))
	now := time.Now()
	c.now = func() time.Time { return now }
	var mints int32
	skew := 5 * time.Minute
	mint := func() (string, time.Time, error) {
		n := atomic.AddInt32(&mints, 1)
		if n == 1 {
			// Expires inside the skew window relative to `now`.
			return "soon-to-expire", now.Add(2 * time.Minute), nil
		}
		return "refreshed-token", now.Add(time.Hour), nil
	}

	if _, err := c.Get(skew, mint); err != nil {
		t.Fatal(err)
	}
	token, err := c.Get(skew, mint)
	if err != nil {
		t.Fatal(err)
	}
	if token != "refreshed-token" {
		t.Errorf("token = %q, want a fresh mint once the cached token entered the skew window", token)
	}
	if mints != 2 {
		t.Errorf("mints = %d, want 2", mints)
	}
}

func TestCache_MintErrorLeavesNoCacheBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path)
	wantErr := errors.New("mint failed")

	_, err := c.Get(5*time.Minute, func() (string, time.Time, error) {
		return "", time.Time{}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("mint error must not leave a cache file behind")
	}
}

// Concurrent Get calls sharing one cache file must serialize through the
// mint, not each mint independently — that is the "locking prevents
// concurrent double-minting" acceptance criterion.
func TestCache_ConcurrentGetsMintOnce(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "cache.json"))
	var mints int32
	mint := func() (string, time.Time, error) {
		atomic.AddInt32(&mints, 1)
		// Widen the critical section so a racy implementation (one that
		// unlocks before minting) has room to interleave a second mint.
		time.Sleep(10 * time.Millisecond)
		return "minted-token", time.Now().Add(time.Hour), nil
	}

	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token, err := c.Get(5*time.Minute, mint)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			results[i] = token
		}(i)
	}
	wg.Wait()

	if mints != 1 {
		t.Errorf("mints = %d, want 1", mints)
	}
	for i, got := range results {
		if got != "minted-token" {
			t.Errorf("results[%d] = %q, want minted-token", i, got)
		}
	}
}

func TestCache_CacheFileModeIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path)

	if _, err := c.Get(5*time.Minute, func() (string, time.Time, error) {
		return "minted-token", time.Now().Add(time.Hour), nil
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file mode = %o, want 0600", perm)
	}
}

// The Linux NFS client rejects LOCK_EX on an O_RDONLY descriptor with EBADF,
// even though local filesystems tolerate it. This test inspects the lock
// file descriptor's own open flags via /proc, so it catches the regression
// even on a local (non-NFS) test filesystem.
func TestCache_UpdateOpensLockFileWritable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lock fd flags are inspected via /proc, which is Linux-specific")
	}

	c := NewCache(filepath.Join(t.TempDir(), "cache.json"))
	lockPath := c.path + ".lock"

	err := c.update(func(*cached) error {
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
