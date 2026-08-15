// Package cliexec implements the plugin's only dependency on the outside
// world: shelling out to the `plect` CLI itself to resolve an orchestrator
// session's status and to create pursue_goal task instances. Every other
// package in this plugin takes that surface as an injected interface, so
// tests never need a real `plect` binary on PATH.
package cliexec

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// CLI shells out to `plect`. Its zero value is ready to use.
type CLI struct{}

// Status runs `plect status <alias> --json --full`, satisfying
// bundle.StatusRunner.
func (CLI) Status(alias string) ([]byte, error) {
	return exec.Command("plect", "status", alias, "--json", "--full").CombinedOutput()
}

// ExistingInstances runs `plect status <session> --json` and reports the
// task instance names already present on that session.
func (CLI) ExistingInstances(session string) (map[string]bool, error) {
	out, err := exec.Command("plect", "status", session, "--json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("plect status %s --json: %w: %s", session, err, out)
	}

	var payload struct {
		Work []struct {
			Instance string `json:"instance"`
		} `json:"work"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parse plect status %s --json: %w", session, err)
	}

	existing := make(map[string]bool, len(payload.Work))
	for _, w := range payload.Work {
		existing[w.Instance] = true
	}
	return existing, nil
}

// SetupPursueGoal runs `plect task setup pursue_goal` for one goal.
func (CLI) SetupPursueGoal(session, name, resourceID string) error {
	out, err := exec.Command("plect", "task", "setup", "pursue_goal",
		"--session", session, "--name", name, "--resource", resourceID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("plect task setup pursue_goal --name %s: %w: %s", name, err, out)
	}
	return nil
}
