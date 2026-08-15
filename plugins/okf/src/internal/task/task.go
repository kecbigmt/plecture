// Package task implements the decision logic behind the plugin's shipped
// tasks: which resource ids a pursue_goal instance may bind to, and which
// open goals goal_bootstrap should instantiate a pursue_goal instance for
// on a given session.
package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/okf/internal/bundle"
	"github.com/kecbigmt/plecture/plugins/okf/internal/goal"
)

// ValidateGoalResource reports an error unless resourceID is a goal
// resource id (`local-okf://<owner>/goals/<concept-id>.md`). pursue_goal's
// setup gates only the resource *kind* — goal-specific completion
// conditions live in the goal file's own "## Done When" checklist, not in
// generated done_when config.
func ValidateGoalResource(resourceID string) error {
	_, conceptID, err := bundle.ParseResourceID(resourceID)
	if err != nil || !strings.HasPrefix(conceptID, "goals/") || !strings.HasSuffix(conceptID, ".md") {
		return fmt.Errorf("pursue_goal requires a local-okf goal resource (--resource local-okf://<owner>/goals/<slug>.md), got: %s", resourceID)
	}
	return nil
}

// InstanceName derives a pursue_goal task instance name from a goal file's
// slug. Instance names must be Go-template identifiers, so a slug's
// hyphens become underscores; a second bootstrap of the same goal then
// collides on the resulting --name instead of silently duplicating.
func InstanceName(slug string) string {
	return "goal_" + strings.ReplaceAll(slug, "-", "_")
}

// Runner is the `plect` CLI surface goal_bootstrap needs: which task
// instances already exist on the session, and how to create a new
// pursue_goal instance.
type Runner interface {
	ExistingInstances(session string) (map[string]bool, error)
	SetupPursueGoal(session, name, resourceID string) error
}

// Bootstrap scans an orchestrator workdir's goals directory and creates a
// missing pursue_goal instance for every open goal the assignee filter
// admits. A pursue_goal instance is session-scoped, so a destroy→up cycle
// silently drops it; bootstrap re-scanning on every `up` is what recreates
// it. It checks for an existing instance before calling setup — not
// cleanup-then-setup — because an existing instance carries recorded judge
// history, and recreating it on every up would re-pend an already-satisfied
// judge for no reason.
func Bootstrap(runner Runner, goalsDir, owner, session string, inputAssignees []string) ([]string, error) {
	entries, err := os.ReadDir(goalsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	existing, err := runner.ExistingInstances(session)
	if err != nil {
		return nil, err
	}

	var created []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(goalsDir, entry.Name())

		fm, ok, err := goal.ReadFrontmatter(path)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Not frontmatter-shaped at all — an index page or similar,
			// not a goal file.
			continue
		}
		status, _ := fm["status"].(string)
		if status != goal.StatusOpen {
			continue
		}

		goalAssignees := toStringSlice(fm["assignee"])
		if !shouldInstantiate(goalAssignees, inputAssignees) {
			continue
		}

		slug := strings.TrimSuffix(entry.Name(), ".md")
		name := InstanceName(slug)
		if existing[name] {
			continue
		}

		resourceID := "local-okf://" + owner + "/goals/" + slug + ".md"
		if err := runner.SetupPursueGoal(session, name, resourceID); err != nil {
			return nil, fmt.Errorf("setup pursue_goal for %s: %w", resourceID, err)
		}
		created = append(created, name)
	}

	return created, nil
}

// shouldInstantiate decides whether a goal's assignee list admits this
// bootstrap run's assignee filter. Assignee scopes a goal to one or more
// actors in a team-shared bundle; the filter is opt-in — an empty input
// list means every open goal instantiates regardless of assignee, keeping
// bootstrap backward compatible with bundles that never declare assignees
// at all. "anyone" is matched as a literal declared assignee value, the
// same as any other; team membership is never resolved, only the literal
// declared string is compared.
func shouldInstantiate(goalAssignees, inputAssignees []string) bool {
	if len(inputAssignees) == 0 || len(goalAssignees) == 0 {
		return true
	}
	input := make(map[string]bool, len(inputAssignees))
	for _, a := range inputAssignees {
		input[a] = true
	}
	for _, g := range goalAssignees {
		if g == "anyone" || input[g] {
			return true
		}
	}
	return false
}

// toStringSlice normalizes YAML's scalar-or-list `assignee` shape (unset,
// a single string, or a list of strings) to a single slice form.
func toStringSlice(v any) []string {
	switch value := v.(type) {
	case nil:
		return nil
	case string:
		return []string{value}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
