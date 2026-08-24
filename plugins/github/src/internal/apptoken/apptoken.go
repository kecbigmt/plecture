// Package apptoken caches one GitHub App installation access token on disk,
// mirroring ratebudget's locked read-modify-write shape: the same file lock
// that protects the read also holds across the mint call, so two `gh`
// invocations racing on an expired cache mint exactly once instead of both
// winning a mint and one write silently discarding the other's token.
package apptoken

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// cached is the on-disk shape. atomicfile.Write creates its temp file with
// mode 0600 (preserved across the rename), which is this package's only
// on-disk copy of the token — the guarantee the gh_app_guard effect's
// acceptance criteria rely on.
type cached struct {
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Cache is one on-disk cache slot for one installation token.
type Cache struct {
	path string
	// now is overridden by tests to freeze the clock.
	now func() time.Time
}

// NewCache creates a Cache backed by path. Callers sharing one installation
// (and wanting one another's mints reused) must point them at the same
// path — there is no implicit default location.
func NewCache(path string) *Cache {
	return &Cache{path: path, now: time.Now}
}

// Get returns a valid token: the cached one if it has more than skew left
// before expiry, otherwise a freshly minted one. mint runs with the cache
// file locked, so a concurrent Get racing on the same path blocks instead of
// minting twice.
func (c *Cache) Get(skew time.Duration, mint func() (token string, expiresAt time.Time, err error)) (string, error) {
	var token string
	err := c.update(func(s *cached) error {
		if s.Token != "" && s.ExpiresAt.After(c.now().Add(skew)) {
			token = s.Token
			return nil
		}
		t, exp, err := mint()
		if err != nil {
			return err
		}
		s.Token, s.ExpiresAt = t, exp
		token = t
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (c *Cache) load() (*cached, error) {
	s := &cached{}
	data, err := os.ReadFile(c.path)
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
		// A corrupt cache degrades to "no token cached" rather than blocking
		// every gh invocation until an operator notices and clears it.
		return &cached{}, nil
	}
	return s, nil
}

// update is the locked read-modify-write primitive, mirroring
// ratebudget.Guard.update: fn runs with the cache file's lock held, so a
// mint fn performs its HTTP round trip inside the critical section.
func (c *Cache) update(fn func(*cached) error) error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(c.path+".lock", os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	s, err := c.load()
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicfile.Write(c.path, data)
}
