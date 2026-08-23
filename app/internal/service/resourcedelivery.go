package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
)

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
