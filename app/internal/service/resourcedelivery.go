package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
)

// subscribeIfWired registers resourceID for event delivery to sessionName —
// binding implies delivery, the runtime counterpart of `plect subscribe` that
// a dynamic task instance's explicit `--resource` triggers automatically.
//
// Unlike Subscribe (an explicit verb the caller means literally), a bound
// resource is not guaranteed to be one any workspace provider governs: an
// opaque --resource with no matching provider, or a matched provider that
// never declared a subscribe hook, is left unwired rather than failing the
// instantiation — the same as before this wiring existed. An ambiguous match
// (the resource fits more than one provider) is a real configuration defect
// and is still surfaced, since silently guessing which one would be worse.
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
// sessionName, the counterpart subscribeIfWired's caller runs once the
// resource is no longer needed (see taskCleanupResourceStillNeeded).
// Resilient by design, matching TaskCleanup's own idempotent-teardown
// posture: no match, an unhooked provider, or an ambiguous match (which
// subscribeIfWired would have refused to wire in the first place) all leave
// cleanup unaffected rather than failing an otherwise-successful teardown.
func unsubscribeIfWired(cfg *config.Config, sessionName, resourceID string) error {
	if strings.TrimSpace(resourceID) == "" {
		return nil
	}
	matched, err := matchWorkspaceProviders(cfg, resourceID)
	if err != nil {
		return err
	}
	if len(matched) != 1 {
		return nil
	}
	prov := matched[0]
	if prov.Unsubscribe == nil {
		return nil
	}
	if hookErr := effect.RunWorkspaceProviderUnsubscribe(prov, effect.SubscribeHookVars{
		ResourceID:  resourceID,
		SessionName: sessionName,
		Plugins:     cfg.Plugins,
		SourcePath:  prov.SourcePath,
	}); hookErr != nil {
		return &Error{Code: ErrExecutionFailed, Message: hookErr.Error()}
	}
	return nil
}
