// Package resource implements the local-okf.goal Resource contract: observe
// reports a goal file's parse and completion state without side effects;
// finalize records completion once done_when has already been reconfirmed
// by the caller. Both compose the bundle and goal packages, and add
// nothing resource-specific of their own beyond the state_schema shape.
package resource

import (
	"path/filepath"
	"time"

	"github.com/kecbigmt/plecture/plugins/okf/internal/bundle"
	"github.com/kecbigmt/plecture/plugins/okf/internal/goal"
)

// Observe outcomes for goal_parse_status.
const (
	ParseStatusSuccess    = "SUCCESS"
	ParseStatusFailure    = "FAILURE"
	ParseStatusUnresolved = "UNRESOLVED"
)

// nullValue is the state_schema sentinel for a field with no goal to report
// on yet, matching the reserved string every downstream done_when check
// compares against.
const nullValue = "NULL"

// ObserveResult is the local-okf.goal state_schema: what `plect resource
// status` and every task built on this resource read.
type ObserveResult struct {
	GoalParseStatus string
	GoalStatus      string
	ChecklistStatus string
	GoalRevision    string
	Revision        string
	OpenItems       string
	ObserveError    string
}

// Observe resolves a `local-okf://<owner>/<concept-id>` resource id to its
// goal file and reports its state. It returns an error only for a
// resolution that must fail outright — an ambiguous owner alias, or a
// concept id that escapes the bundle — because those have no state to fold
// into. Every other failure to locate the goal (no session, unreadable
// workdir, missing bundle, missing file) becomes an UNRESOLVED result, and
// a malformed file becomes a FAILURE result: both are reported, not
// returned as errors, because observe is a status read, not a command that
// can fail outright.
func Observe(runner bundle.StatusRunner, resourceID string) (*ObserveResult, error) {
	owner, conceptID, err := bundle.ParseResourceID(resourceID)
	if err != nil {
		return nil, err
	}

	_, path, rerr := resolve(runner, owner, conceptID)
	if rerr != nil {
		if !rerr.Unresolved {
			return nil, rerr
		}
		return &ObserveResult{
			GoalParseStatus: ParseStatusUnresolved,
			GoalStatus:      nullValue,
			ChecklistStatus: nullValue,
			ObserveError:    rerr.Reason,
		}, nil
	}

	g, parseErr := goal.Parse(path)
	if parseErr != nil {
		status := parseErr.Status
		if status == "" {
			status = nullValue
		}
		return &ObserveResult{
			GoalParseStatus: ParseStatusFailure,
			GoalStatus:      status,
			ChecklistStatus: nullValue,
			GoalRevision:    parseErr.Revision,
			Revision:        parseErr.Revision,
			ObserveError:    parseErr.Reason,
		}, nil
	}

	return &ObserveResult{
		GoalParseStatus: ParseStatusSuccess,
		GoalStatus:      g.Status,
		ChecklistStatus: g.ChecklistStatus,
		GoalRevision:    g.Revision,
		Revision:        g.Revision,
		OpenItems:       g.OpenItems,
	}, nil
}

// Finalize records a goal's completion. Unlike Observe, every resolution
// failure here is a hard error: finalize is a write, and a write that
// cannot find its target has no "pending" state to report — it must fail
// outright, matching `plect task finalize`'s own "gate, then record, or
// fail" contract.
func Finalize(runner bundle.StatusRunner, resourceID, revision string, now time.Time, judges []goal.Judge) error {
	owner, conceptID, err := bundle.ParseResourceID(resourceID)
	if err != nil {
		return err
	}

	root, path, rerr := resolve(runner, owner, conceptID)
	if rerr != nil {
		return rerr
	}

	logPath := filepath.Join(root, "log.md")
	return goal.Finalize(path, logPath, resourceID, revision, now, judges)
}

// resolve is the resolution chain observe and finalize share: owner alias
// to workdir, workdir to bundle root, root plus concept id to a contained
// file. It returns the root alongside the file path so finalize can place
// the completion log entry beside the bundle without a second `plect
// status` round trip.
func resolve(runner bundle.StatusRunner, owner, conceptID string) (root, path string, rerr *bundle.ResolveError) {
	workdir, rerr := bundle.ResolveOwnerWorkdir(runner, owner)
	if rerr != nil {
		return "", "", rerr
	}
	root, rerr = bundle.Root(workdir)
	if rerr != nil {
		return "", "", rerr
	}
	path, rerr = bundle.ResolveConceptPath(root, conceptID)
	if rerr != nil {
		return "", "", rerr
	}
	return root, path, nil
}
