package service

import (
	"maps"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// TemplateVars is the read-only projection of a session that `plect template
// render --session` needs: just enough to populate a template's session vars,
// without handing the raw mutating-lifecycle-owned *domain.Session to a
// surface adapter that only wants to render text.
type TemplateVars struct {
	SessionName   string
	ResourceID    string
	WorktreePath  string
	Workflow      map[string]any
	SessionInputs map[string]any
}

// ResolveTemplateVars resolves identifier to a session (the same lookup order
// as ResolveSession) and projects it into TemplateVars.
func ResolveTemplateVars(cfg *config.Config, store *state.Store, identifier string) (*TemplateVars, error) {
	name, session, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return nil, err
	}
	vars := projectTemplateVars(name, session)
	return &vars, nil
}

// projectTemplateVars builds TemplateVars from a resolved session.
func projectTemplateVars(name string, s *domain.Session) TemplateVars {
	var outputs map[string]any
	if s.Tasks != nil {
		if wf := s.Tasks[contract.WorkflowPseudoNodeID]; wf != nil {
			outputs = wf.Outputs
		}
	}
	inputs := maps.Clone(s.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	return TemplateVars{
		SessionName:   name,
		ResourceID:    s.ResourceID,
		WorktreePath:  s.WorktreePath,
		Workflow:      outputs,
		SessionInputs: inputs,
	}
}
