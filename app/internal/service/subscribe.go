package service

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// SubscribeParams are the inputs to Subscribe. ResourceID is the opaque
// resource the caller wants this session to receive events from. SessionName
// defaults to the ambient pane env ($PLECT_SESSION_NAME, exported by the
// interactive-agent task) when left empty, so a running agent can `plect
// subscribe <url>` without naming itself.
type SubscribeParams struct {
	ResourceID  string
	SessionName string
	Inputs      map[string]any
}

// Subscribe binds a session to a resource through its declaration's hook.
func Subscribe(cfg *config.Config, store *state.Store, params SubscribeParams) error {
	if strings.TrimSpace(params.ResourceID) == "" {
		return &Error{Code: ErrInvalidInput, Message: "resource id is required"}
	}

	sessionName := params.SessionName
	if sessionName == "" {
		sessionName = os.Getenv("PLECT_SESSION_NAME")
	}
	if sessionName == "" {
		return &Error{Code: ErrInvalidInput, Message: "no session in scope: run inside a plect session pane (PLECT_SESSION_NAME) or pass --session"}
	}
	if err := validateSessionName(sessionName); err != nil {
		return err
	}
	// The subscriber must be a real session: the watcher delivers events into
	// its event log, so a typo'd --session would create a ghost subscription
	// publishing to a name nothing reads. The env-default path always names
	// the caller's own (existing) session; this guards the explicit override.
	session, err := store.GetE(sessionName)
	if err != nil {
		return err
	}
	if session == nil {
		return &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q does not exist", sessionName)}
	}

	def, err := resourceDefForSubscription(cfg, params.ResourceID)
	if err != nil {
		return err
	}
	if err := validateSubscriptionInputs(def, params.Inputs); err != nil {
		return &Error{Code: ErrInvalidInput, Message: err.Error()}
	}

	if hookErr := effect.RunResourceSubscribe(def, effect.SubscribeHookVars{
		ResourceID:  params.ResourceID,
		Inputs:      params.Inputs,
		SessionName: sessionName,
		Plugins:     cfg.Plugins,
		SourcePath:  def.SourcePath,
	}); hookErr != nil {
		return &Error{Code: ErrExecutionFailed, Message: hookErr.Error()}
	}
	return nil
}

func validateSubscriptionInputs(def config.ResourceDef, inputs map[string]any) error {
	return task.ValidateResourceInputs(def, inputs)
}

// matchResourceDefs keeps all matches, including declarations without hooks,
// so a missing hook cannot hide an ambiguous resource type.
func matchResourceDefs(cfg *config.Config, resource string) ([]config.ResourceDef, error) {
	defs, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load resources: %v", err)}
	}
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var matched []config.ResourceDef
	for _, id := range ids {
		def := defs[id]
		re, reErr := regexp.Compile(def.Match)
		if reErr != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("resource %q match: %v", def.ID, reErr)}
		}
		if re.MatchString(resource) {
			matched = append(matched, def)
		}
	}
	return matched, nil
}

// resourceDefForSubscription requires exactly one matching declaration with
// a subscribe hook.
func resourceDefForSubscription(cfg *config.Config, resource string) (config.ResourceDef, error) {
	matched, err := matchResourceDefs(cfg, resource)
	if err != nil {
		return config.ResourceDef{}, err
	}
	switch len(matched) {
	case 0:
		return config.ResourceDef{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no resource declaration matches %q", resource)}
	case 1:
		def := matched[0]
		if def.Subscribe == nil {
			return config.ResourceDef{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q does not support subscribe", def.ID)}
		}
		return def, nil
	default:
		names := make([]string, len(matched))
		for i, p := range matched {
			names[i] = p.ID
		}
		return config.ResourceDef{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches multiple resource declarations (%s)", resource, strings.Join(names, ", "))}
	}
}
