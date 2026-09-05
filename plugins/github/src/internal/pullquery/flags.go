package pullquery

import (
	"encoding/json"
	"fmt"
)

// ParseInputs takes draft as a string, not a bool: a config-rendered exec
// action passes "--draft" and its value as two separate argv elements, and
// Go's flag package only accepts a bool flag's value joined as
// "-draft=false" — the space-separated form is read as bare "-draft"
// (true) followed by an unrelated positional argument.
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
