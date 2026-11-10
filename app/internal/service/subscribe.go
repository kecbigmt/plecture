package service

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type SubscribeParams struct {
	ResourceID  string
	SessionName string
	Inputs      map[string]any
}

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
	session, err := store.GetE(sessionName)
	if err != nil {
		return err
	}
	if session == nil {
		return &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q does not exist", sessionName)}
	}

	def, err := resourceDefForSubscription(cfg, session.ProjectRoot, params.ResourceID)
	if err != nil {
		return err
	}
	if err := validateSubscriptionInputs(def, params.Inputs); err != nil {
		return &Error{Code: ErrInvalidInput, Message: err.Error()}
	}

	name, matched, err := def.InstanceName(params.ResourceID)
	if err != nil {
		return &Error{Code: ErrInvalidInput, Message: err.Error()}
	}
	if !matched {
		return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q does not match its declaration", params.ResourceID)}
	}
	resource := &contract.Resource{Type: def.ID, Name: name, Locator: params.ResourceID}
	if err := store.AddSubscription(sessionName, resource, "runtime", params.Inputs, def.MaxSubscribers); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return nil
}

func Unsubscribe(cfg *config.Config, store *state.Store, params SubscribeParams) error {
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
	name := ""
	if session, err := store.GetE(sessionName); err != nil {
		return err
	} else if session != nil && cfg != nil {
		matched, err := matchResourceDefs(cfg, session.ProjectRoot, params.ResourceID)
		if err != nil {
			return err
		}
		if len(matched) == 1 {
			name, _, err = matched[0].InstanceName(params.ResourceID)
			if err != nil {
				return err
			}
		}
	}
	if err := store.RemoveSubscriptionByIdentity(sessionName, params.ResourceID, name, "runtime"); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return nil
}

func validateSubscriptionInputs(def config.ResourceDef, inputs map[string]any) error {
	if inputs == nil {
		inputs = map[string]any{}
	}
	if def.InputsSchema == nil {
		if len(inputs) > 0 {
			return fmt.Errorf("resource %q declares no inputs_schema", def.ID)
		}
		return nil
	}
	schema, err := lang.CompileSchema(def.InputsSchema, "", "plect:resource:"+def.ID+":inputs")
	if err != nil {
		return fmt.Errorf("resource %q inputs_schema: %w", def.ID, err)
	}
	if err := schema.Validate(inputs); err != nil {
		return fmt.Errorf("resource %q inputs: %w", def.ID, err)
	}
	return nil
}

func matchResourceDefs(cfg *config.Config, projectRoot, resource string) ([]config.ResourceDef, error) {
	defs, err := cfg.LoadResourceDefs(projectRoot)
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

// resourceDefForSubscription requires exactly one matching declaration.
func resourceDefForSubscription(cfg *config.Config, projectRoot, resource string) (config.ResourceDef, error) {
	matched, err := matchResourceDefs(cfg, projectRoot, resource)
	if err != nil {
		return config.ResourceDef{}, err
	}
	switch len(matched) {
	case 0:
		return config.ResourceDef{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no resource declaration matches %q", resource)}
	case 1:
		return matched[0], nil
	default:
		names := make([]string, len(matched))
		for i, p := range matched {
			names[i] = p.ID
		}
		return config.ResourceDef{}, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches multiple resource declarations (%s)", resource, strings.Join(names, ", "))}
	}
}
