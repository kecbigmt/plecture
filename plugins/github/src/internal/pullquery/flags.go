package pullquery

import (
	"encoding/json"
	"fmt"
)

// ParseInputs decodes the query's shared inputs_schema from the flag shape
// both query-pulls and subscribe-pulls accept: `repositories`/`labels` as
// JSON arrays (matching the ADR's `{ json = { from = ... } }` argument
// rendering), `state` as a plain string, and `draft` as the literal string
// "true" or "false" — the ADR's own sketch renders it with
// `{ expr = "inputs.draft ? 'true' : 'false'" }` as one argv element
// separate from `--draft`, which rules out a `flag.Bool`: Go's flag package
// only accepts a bool flag's value joined as `-draft=false`, not the
// two-argv-element `-draft false` shape a rendered exec action produces
// (that shape is instead read as bare `-draft` followed by an unrelated
// positional argument).
func ParseInputs(repositoriesJSON, labelsJSON, state, draft string) (Inputs, error) {
	var repositories []string
	if err := json.Unmarshal([]byte(repositoriesJSON), &repositories); err != nil {
		return Inputs{}, fmt.Errorf("parse --repositories: %w", err)
	}
	var labels []string
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		return Inputs{}, fmt.Errorf("parse --labels: %w", err)
	}
	if err := ValidateState(state); err != nil {
		return Inputs{}, err
	}
	draftBool, err := parseDraft(draft)
	if err != nil {
		return Inputs{}, err
	}
	return Inputs{Repositories: repositories, Labels: labels, State: state, Draft: draftBool}, nil
}

func parseDraft(draft string) (bool, error) {
	switch draft {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid --draft %q: want \"true\" or \"false\"", draft)
	}
}
