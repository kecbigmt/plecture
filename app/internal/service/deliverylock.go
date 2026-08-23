package service

import (
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/kecbigmt/plecture/app/internal/state"
)

// withDeliveryLock serializes every subscribe/unsubscribe decision-and-act
// sequence for one session behind a single exclusive file lock, so
// TaskSetup's "bind and subscribe" and TaskCleanup's "is this still needed,
// and if not, unsubscribe" can never interleave for that session: whichever
// starts first runs to completion — including its own unlocked hook call —
// before the other even reads session state for its own decision.
//
// A fresh state read immediately before an unsubscribe hook call (what
// TaskCleanup did before this existed) only narrows that window; it cannot
// close it, since a concurrent subscribe can still land in the gap between
// the read and the hook call. The watcher's own registry has no "delete
// only if unreferenced" primitive to lean on instead, so the only way to
// close the race without changing that registry is to never let the two
// decisions run concurrently at all — which a lock scoped to app's own
// session-state directory can guarantee on its own.
func withDeliveryLock(store *state.Store, sessionName string, fn func()) error {
	path := filepath.Join(store.Dir(), "delivery-locks", url.PathEscape(sessionName)+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	fn()
	return nil
}
