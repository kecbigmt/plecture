package service

import (
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// ResourceStatusParams are the inputs to ResourceStatus (`plect resource
// status`).
type ResourceStatusParams struct {
	ResourceID string
}

// ResourceStatusResult reports one resource observation.
type ResourceStatusResult struct {
	ResourceID string         `json:"resource_id"`
	Definition string         `json:"definition"`
	State      map[string]any `json:"state"`
}

// ResourceStatus observes an external resource by resource id: it finds the
// trusted resource definition (resources/*.toml) whose `match` recognizes the
// id and runs its `observe` script. This is the same observation contract a
// task instance's from_resource_status dynamic output reads from — 'plect
// resource status' just lets it be read standalone (ADR "goal-as-task" D1:
// core only knows that a resource id maps to a JSON state snapshot, never the
// storage it came from).
func ResourceStatus(cfg *config.Config, params ResourceStatusParams) (*ResourceStatusResult, error) {
	resourceID := strings.TrimSpace(params.ResourceID)
	if resourceID == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "resource id is required"}
	}
	defs, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	state, def, ok, serr := task.ResourceStatus(defs, resourceID, "", "", cfg.Plugins)
	if serr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: serr.Error()}
	}
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: "no resource definition recognizes " + resourceID}
	}
	return &ResourceStatusResult{ResourceID: resourceID, Definition: def.ID, State: state}, nil
}
