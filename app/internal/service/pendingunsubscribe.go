package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// pendingUnsubscribeFile durably queues, per session, the resources a
// TaskCleanup determined were no longer needed but could not actually
// unregister — a failed unsubscribe hook, or a failed freshness re-check —
// so the failure is a retry candidate on the next activity for that
// session rather than a lost one-shot attempt. It is rooted next to
// state.json but kept out of contracts/state.Session itself: that type is a
// separately versioned, pseudo-version-pinned module (see this repo's
// installability invariant), and this queue is a pure implementation detail
// of this package's own retry behavior, not part of that public contract.
type pendingUnsubscribeFile struct {
	Resources map[string][]string `json:"resources,omitempty"`
}

func pendingUnsubscribePath(store *state.Store) string {
	return filepath.Join(store.Dir(), "pending_unsubscribe.json")
}

func loadPendingUnsubscribe(path string) *pendingUnsubscribeFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return &pendingUnsubscribeFile{}
	}
	f := &pendingUnsubscribeFile{}
	if err := json.Unmarshal(data, f); err != nil {
		// A corrupt queue file blocks nothing that isn't already queued —
		// treating it as empty loses at most the pending entries, never the
		// instance cleanup that already succeeded.
		return &pendingUnsubscribeFile{}
	}
	return f
}

// updatePendingUnsubscribe runs fn under an exclusive file lock shared with
// every other process touching this queue — the same locked
// read-modify-write shape state.Store already uses for a JSON file several
// processes may touch concurrently.
func updatePendingUnsubscribe(path string, fn func(*pendingUnsubscribeFile)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	f := loadPendingUnsubscribe(path)
	fn(f)
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data)
}

// queuePendingUnsubscribe records resource as still owed for sessionName.
// Best-effort: a failure to persist the queue entry is not itself queueable,
// so it is swallowed here — the caller already has its own UnsubscribeError
// to report, and this is strictly an additional safety net on top of that.
func queuePendingUnsubscribe(store *state.Store, sessionName, resource string) {
	_ = updatePendingUnsubscribe(pendingUnsubscribePath(store), func(f *pendingUnsubscribeFile) {
		if f.Resources == nil {
			f.Resources = map[string][]string{}
		}
		if !slices.Contains(f.Resources[sessionName], resource) {
			f.Resources[sessionName] = append(f.Resources[sessionName], resource)
		}
	})
}

func dequeuePendingUnsubscribe(store *state.Store, sessionName, resource string) {
	_ = updatePendingUnsubscribe(pendingUnsubscribePath(store), func(f *pendingUnsubscribeFile) {
		f.Resources[sessionName] = slices.DeleteFunc(f.Resources[sessionName], func(r string) bool { return r == resource })
		if len(f.Resources[sessionName]) == 0 {
			delete(f.Resources, sessionName)
		}
	})
}

// flushPendingUnsubscribes retries every resource queued for sessionName.
// Called opportunistically at the top of TaskSetup/TaskCleanup rather than
// from a background process: ordinary session activity is what eventually
// drains the queue, and a retry that still cannot run (an unreachable
// provider, a state read that fails) simply leaves its entry queued for the
// next opportunity instead of losing it a second time. A resource that
// turns out to be needed again (a new instance bound to it since the
// original failure) is dropped from the queue without running the hook —
// the failure that queued it is moot once something else has since claimed
// the resource.
func flushPendingUnsubscribes(cfg *config.Config, store *state.Store, sessionName string) {
	f := loadPendingUnsubscribe(pendingUnsubscribePath(store))
	pending := f.Resources[sessionName]
	if len(pending) == 0 {
		return
	}
	for _, resource := range slices.Clone(pending) {
		fresh, freshErr := store.GetE(sessionName)
		if freshErr != nil || fresh == nil {
			continue
		}
		if taskCleanupResourceStillNeeded(fresh, resource) {
			dequeuePendingUnsubscribe(store, sessionName, resource)
			continue
		}
		if _, unsubErr := unsubscribeIfWired(cfg, sessionName, resource); unsubErr == nil {
			dequeuePendingUnsubscribe(store, sessionName, resource)
		}
	}
}
