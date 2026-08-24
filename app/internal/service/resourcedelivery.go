package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// wireDeliveryOnSetup is TaskSetup's/setupTaskDocument's/
// createWithWorkflowSetup's shared entry point for binding-implies-delivery:
// it runs subscribeIfWired under withDeliveryLock (excluding any concurrent
// TaskCleanup's own unsubscribe decision for this session, closing the race
// a fresh read alone could only narrow), and on failure queues the resource
// for a durable retry rather than losing the one-shot attempt — mirroring
// TaskCleanup's own queuePendingUnsubscribe path. The queue call sits
// outside withDeliveryLock's callback so a failure to acquire the lock
// itself is queued too, the same as a failure inside it; only the lock's
// own success or failure decides whether subscribeIfWired ran, not whether
// a retry gets queued. Never returns an error: every caller treats a wiring
// failure as non-fatal to the instantiation that already succeeded,
// reporting it via the returned message instead.
func wireDeliveryOnSetup(cfg *config.Config, store *state.Store, sessionName, resource string) (bool, string) {
	if strings.TrimSpace(resource) == "" {
		return false, ""
	}
	var subscribed bool
	var errMsg string
	if lockErr := withDeliveryLock(store, sessionName, func() {
		var subErr error
		subscribed, subErr = subscribeIfWired(cfg, sessionName, resource)
		if subErr != nil {
			errMsg = subErr.Error()
		}
	}); lockErr != nil {
		errMsg = fmt.Sprintf("could not acquire the delivery lock: %v", lockErr)
	}
	if errMsg != "" {
		if queueErr := queuePendingSubscribe(store, sessionName, resource); queueErr != nil {
			errMsg = fmt.Sprintf("%s (and failed to durably queue a retry: %v)", errMsg, queueErr)
		}
	}
	return subscribed, errMsg
}

// subscribeIfWired registers resourceID for event delivery to sessionName,
// the runtime counterpart of `plect subscribe` a dynamic task instance's
// explicit `--resource` triggers automatically (binding implies delivery).
//
// Unlike an explicit `plect subscribe` call, a bound resource is not
// guaranteed to be one any workspace provider governs: no matching provider,
// or a matched provider with no subscribe hook, returns (false, nil) rather
// than an error, since neither means the caller did anything wrong. An
// ambiguous match (the resource fits more than one provider) is a real
// configuration defect and is still reported as an error — TaskSetup treats
// it, like a hook execution failure, as non-fatal (see its own comment),
// since the instance it would otherwise leave orphaned has already been
// fully instantiated by the time this runs.
func subscribeIfWired(cfg *config.Config, sessionName, resourceID string) (bool, error) {
	if strings.TrimSpace(resourceID) == "" {
		return false, nil
	}
	matched, err := matchWorkspaceProviders(cfg, resourceID)
	if err != nil {
		return false, err
	}
	switch len(matched) {
	case 0:
		return false, nil
	case 1:
		prov := matched[0]
		if prov.Subscribe == nil {
			return false, nil
		}
		if hookErr := effect.RunWorkspaceProviderSubscribe(prov, effect.SubscribeHookVars{
			ResourceID:  resourceID,
			SessionName: sessionName,
			Plugins:     cfg.Plugins,
			SourcePath:  prov.SourcePath,
		}); hookErr != nil {
			return false, &Error{Code: ErrExecutionFailed, Message: hookErr.Error()}
		}
		return true, nil
	default:
		names := make([]string, len(matched))
		for i, p := range matched {
			names[i] = p.ID
		}
		return false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches multiple workspace providers (%s)", resourceID, strings.Join(names, ", "))}
	}
}

// unsubscribeIfWired drops resourceID's event-delivery registration for
// sessionName — subscribeIfWired's counterpart, which TaskCleanup runs once
// the resource is no longer needed. The bool return distinguishes "ran the
// unsubscribe hook" from "nothing to do" (no match, an unhooked provider, or
// an ambiguous match, which subscribeIfWired would itself have refused to
// wire): both report (false, nil), so a caller cannot mistake silence for
// having actually dropped a registration.
func unsubscribeIfWired(cfg *config.Config, sessionName, resourceID string) (bool, error) {
	if strings.TrimSpace(resourceID) == "" {
		return false, nil
	}
	matched, err := matchWorkspaceProviders(cfg, resourceID)
	if err != nil {
		return false, err
	}
	if len(matched) != 1 {
		return false, nil
	}
	prov := matched[0]
	if prov.Unsubscribe == nil {
		return false, nil
	}
	if hookErr := effect.RunWorkspaceProviderUnsubscribe(prov, effect.SubscribeHookVars{
		ResourceID:  resourceID,
		SessionName: sessionName,
		Plugins:     cfg.Plugins,
		SourcePath:  prov.SourcePath,
	}); hookErr != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: hookErr.Error()}
	}
	return true, nil
}
