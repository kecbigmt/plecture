package service

import (
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/inbox"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// inboxPollInterval trades take latency against one cheap read per interval
// for every waiting runtime.
const inboxPollInterval = 250 * time.Millisecond

// inboxSession checks the guard before existence, so an out-of-scope name is
// rejected without telling the caller whether that session exists.
func inboxSession(cfg *config.Config, store *state.Store, identifier string) (string, error) {
	name, err := resolveSessionName(cfg, store, identifier)
	if err != nil {
		return "", err
	}
	if guardErr := checkSessionGuard(cfg, name); guardErr != nil {
		return "", guardErr
	}
	if session, err := store.GetE(name); err != nil {
		return "", err
	} else if session == nil {
		return "", &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("unknown session %q", name)}
	}
	return name, nil
}

// InboxTake returns nil when nothing became pending within wait.
func InboxTake(cfg *config.Config, store *state.Store, identifier string, wait time.Duration) (*inbox.Batch, error) {
	name, err := inboxSession(cfg, store, identifier)
	if err != nil {
		return nil, err
	}
	box := inbox.NewStore(store.Dir())
	deadline := time.Now().Add(wait)
	for {
		// The wait polls with a read: a take is a write transaction, and
		// every idle runtime would otherwise contend for the write lock.
		pending, err := box.Pending(name)
		if err != nil {
			return nil, err
		}
		if pending {
			batch, err := box.Take(name)
			if err != nil || batch != nil {
				return batch, err
			}
		}
		if !time.Now().Before(deadline) {
			return nil, nil
		}
		time.Sleep(inboxPollInterval)
	}
}

func InboxAck(cfg *config.Config, store *state.Store, identifier, batchID string) error {
	name, err := inboxSession(cfg, store, identifier)
	if err != nil {
		return err
	}
	return inbox.NewStore(store.Dir()).Ack(name, batchID)
}
