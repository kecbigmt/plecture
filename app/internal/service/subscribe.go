package service

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
)

// SubscribeParams are the inputs to Subscribe. ResourceID is the opaque
// resource the caller wants this session to receive events from. SessionName
// defaults to the ambient pane env ($SENNIT_SESSION_NAME, exported by the claude
// task) when left empty, so a running agent can `sennit subscribe <url>` without
// naming itself.
type SubscribeParams struct {
	ResourceID  string
	SessionName string
}

// Subscribe binds the current session to an opaque resource so the session
// starts receiving that resource's events. It is the runtime counterpart to
// the dispatch-time auto-subscribe task: additive, never
// replacing — subscribing a resource another session already watches does not
// take it over.
//
// core stays provider-agnostic: it matches the resource against each
// provider's resolver to pick the owning provider, fills in the current
// session from the ambient env, and runs that provider's `subscribe` hook.
// Everything resource-specific (registering with a resident watcher, say)
// lives in the hook — core never parses the resource.
func Subscribe(cfg *config.Config, store *state.Store, params SubscribeParams) error {
	if strings.TrimSpace(params.ResourceID) == "" {
		return &Error{Code: ErrInvalidInput, Message: "resource id is required"}
	}

	sessionName := params.SessionName
	if sessionName == "" {
		sessionName = os.Getenv("SENNIT_SESSION_NAME")
	}
	if sessionName == "" {
		return &Error{Code: ErrInvalidInput, Message: "no session in scope: run inside a sennit session pane (SENNIT_SESSION_NAME) or pass --session"}
	}
	if err := validateSessionName(sessionName); err != nil {
		return err
	}
	// The subscriber must be a real session: the watcher delivers events into
	// its event log, so a typo'd --session would create a ghost subscription
	// publishing to a name nothing reads. The env-default path always names
	// the caller's own (existing) session; this guards the explicit override.
	if store.Get(sessionName) == nil {
		return &Error{Code: ErrWorkspaceNotFound, Message: fmt.Sprintf("session %q does not exist", sessionName)}
	}

	prov, err := providerForResource(cfg, params.ResourceID)
	if err != nil {
		return err
	}

	if hookErr := task.RunProviderSubscribe(prov, task.SubscribeHookVars{
		ResourceID:  params.ResourceID,
		SessionName: sessionName,
	}); hookErr != nil {
		return &Error{Code: ErrExecutionFailed, Message: hookErr.Error()}
	}
	return nil
}

// providerForResource selects the single provider whose resolver matches the
// resource. Mirrors dispatchResource's no-flag branch but keys on the
// provider's `match` directly (subscribe has no workflow): zero matches is an
// "unknown resource" error, several is ambiguity. The chosen provider must
// declare a `subscribe` hook.
func providerForResource(cfg *config.Config, resource string) (config.ProviderConfig, error) {
	providers, err := cfg.LoadProviders()
	if err != nil {
		return config.ProviderConfig{}, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load providers: %v", err)}
	}
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var matched []config.ProviderConfig
	for _, id := range ids {
		prov := providers[id]
		if !prov.HasResolver() {
			continue
		}
		re, reErr := regexp.Compile(prov.Match)
		if reErr != nil {
			return config.ProviderConfig{}, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("provider %q resolver match: %v", prov.ID, reErr)}
		}
		if re.MatchString(resource) {
			matched = append(matched, prov)
		}
	}
	switch len(matched) {
	case 0:
		return config.ProviderConfig{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no provider matches resource %q", resource)}
	case 1:
		prov := matched[0]
		if strings.TrimSpace(prov.Subscribe) == "" {
			return config.ProviderConfig{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("provider %q does not support subscribe", prov.ID)}
		}
		return prov, nil
	default:
		names := make([]string, len(matched))
		for i, p := range matched {
			names[i] = p.ID
		}
		return config.ProviderConfig{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches multiple providers (%s)", resource, strings.Join(names, ", "))}
	}
}
