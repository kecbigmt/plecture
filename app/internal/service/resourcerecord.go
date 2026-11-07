package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// A session takes its name from a resource name, so a name the session-name
// rule refuses is refused where it is derived rather than where it is used.
func validateResourceName(address, name string) *Error {
	if !validSessionNameRE.MatchString(name) {
		return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q derived the name %q, which is not a valid resource name: it must match %s", address, name, validSessionNameRE.String())}
	}
	return nil
}

// The recorded row replaces derived, so a later spelling keeps the first
// locator.
func recordedResource(store *state.Store, derived contract.Resource) (resource contract.Resource, conflict *Error, err error) {
	recorded, err := store.ResourceByName(derived.Name)
	if err != nil {
		return derived, nil, fmt.Errorf("read resource %q: %w", derived.Name, err)
	}
	if recorded == nil {
		return derived, nil, nil
	}
	if recorded.Type != derived.Type {
		return derived, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q derives the name %q from %q, which %s already holds", derived.Type, derived.Name, derived.Locator, describeResource(*recorded))}, nil
	}
	return *recorded, nil, nil
}

// ResolveResource reports matched false, with no error, when the declaration
// at address does not recognize identifier.
func ResolveResource(store *state.Store, address string, def config.ResourceDef, identifier string) (resource contract.Resource, matched bool, err error) {
	name, matched, nameErr := def.InstanceName(identifier)
	if nameErr != nil {
		return contract.Resource{}, false, &Error{Code: ErrInvalidInput, Message: nameErr.Error()}
	}
	if !matched {
		return contract.Resource{}, false, nil
	}
	if err := validateResourceName(address, name); err != nil {
		return contract.Resource{}, true, err
	}
	recorded, conflict, readErr := recordedResource(store, contract.Resource{Type: address, Name: name, Locator: identifier})
	if readErr != nil {
		return contract.Resource{}, true, &Error{Code: ErrExecutionFailed, Message: readErr.Error()}
	}
	if conflict != nil {
		return contract.Resource{}, true, conflict
	}
	return recorded, true, nil
}

// An identifier that no declaration, or more than one, recognizes is still
// bound, so it is recorded without a type.
func boundResource(cfg *config.Config, store *state.Store, projectRoot, identifier string) (*contract.Resource, *Error) {
	if identifier == "" {
		return nil, nil
	}
	defs, err := cfg.LoadResourceDefs(projectRoot)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load resources: %v", err)}
	}
	address, def, matched, matchErr := task.MatchResourceDef(defs, identifier)
	if matchErr != nil || !matched {
		return &contract.Resource{Locator: identifier}, nil
	}
	resource, _, resolveErr := ResolveResource(store, address, def, identifier)
	if resolveErr != nil {
		return nil, asServiceError(resolveErr)
	}
	return &resource, nil
}

func describeResource(r contract.Resource) string {
	if r.Type == "" {
		return fmt.Sprintf("identifier %q, which no resource declaration resolved", r.Locator)
	}
	return fmt.Sprintf("resource %q name %q", r.Type, r.Name)
}

// An equal locator also counts, because an identifier no declaration resolved
// has a row of its own beside any typed row recorded for it later.
func sameResource(recorded, bound *contract.Resource) bool {
	switch {
	case recorded == nil || bound == nil:
		return recorded == nil && bound == nil
	case recorded.Locator == bound.Locator:
		return true
	default:
		return bound.Name != "" && recorded.Name == bound.Name
	}
}

// ResourceView is a resource as output surfaces show it. The id is a storage
// key no reader can look a resource up by, so it is left out.
type ResourceView struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Locator string `json:"locator"`
}

func resourceView(r *contract.Resource) *ResourceView {
	if r == nil {
		return nil
	}
	return &ResourceView{Type: r.Type, Name: r.Name, Locator: r.Locator}
}
