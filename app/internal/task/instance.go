package task

import (
	"fmt"
	"strconv"
	"strings"

	contract "github.com/kecbigmt/sennit/contracts/state"
)

// InstanceKey derives the session.Tasks key for the numbered form of a dynamic
// task instance (ADR-003): "<taskID>#<instanceID>", where instanceID is a
// per-task sequential number (see NextInstanceNumber). This is the `--name`-
// less form; a `--name` instance keys on the name alone (ValidInstanceName), so
// the two namespaces are disjoint (the named form carries no `#`). A blank
// instanceID degrades to the bare task id (used only in tests that key tasks
// directly).
func InstanceKey(taskID, instanceID string) string {
	if strings.TrimSpace(instanceID) == "" {
		return taskID
	}
	return taskID + "#" + instanceID
}

// ValidInstanceName reports whether a `--name` is a usable instance key: it must
// match nodeIDRE (a Go-template identifier, no `#`), so a named key can never
// collide with the numbered "<task>#<n>" namespace nor interfere with
// NextInstanceNumber's suffix scan.
func ValidInstanceName(name string) bool {
	return nodeIDRE.MatchString(name)
}

// NextInstanceNumber returns the next per-task instance number for taskID:
// one past the highest integer suffix among existing "<taskID>#<n>" keys in
// the session. Numbering is per-task and 1-based ("review#1", "review#2", …;
// "build" numbers independently). Cleaned instances remain in state, so numbers
// are monotonic and never reused — a re-instantiation always gets a fresh,
// higher number. Keys whose suffix is not an integer are ignored.
//
// The session is single-machine and the key is session-local (not a capability,
// so it need not be unguessable); the caller allocates the number under the
// state lock so concurrent `sennit task setup`s cannot collide.
func NextInstanceNumber(taskID string, tasks map[string]*contract.TaskState) int {
	prefix := taskID + "#"
	max := 0
	for key := range tasks {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		n, err := strconv.Atoi(key[len(prefix):])
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max + 1
}

// ExecuteTaskSetup runs a dynamic instance's setup — input-schema validation,
// template render, shell, output parse, and output-schema validation — and
// returns the parsed outputs. It does NOT touch session state: the caller
// reserves the instance key under the state lock, runs this WITHOUT the lock
// (setup may shell out for a while), then merges the result back under the lock.
// stderr is returned so the caller's observer can surface diagnostic output.
//
// inputs are the already-bound input values (the caller applies the
// --input > provider/workflow outputs > session vars precedence). workflowTasks
// is the session's tasks map, read only to expose the @workflow (and
// @environment) pseudo-nodes' outputs to the setup template.
//
// envExecutor is optional (variadic so every pre-existing call site keeps
// compiling unchanged): when supplied, a resolved Execution of "environment"
// runs through it instead of the host.
func ExecuteTaskSetup(r Resolved, inputs map[string]any, session SessionVars, workflowTasks map[string]*contract.TaskState, envExecutor ...Executor) (map[string]any, []byte, error) {
	if inputs == nil {
		inputs = map[string]any{}
	}
	if r.InputsSchema != nil {
		if vErr := r.InputsSchema.Validate(toJSONShape(inputs)); vErr != nil {
			return nil, nil, fmt.Errorf("input schema: %w", vErr)
		}
	}
	ctx := RenderContext{
		Self:        map[string]any{},
		Inputs:      inputs,
		Workflow:    workflowOutputs(workflowTasks),
		Environment: environmentOutputs(workflowTasks),
		Session:     session,
	}
	cmdStr, err := render(r.Setup, ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("setup template: %w", err)
	}

	outputs := map[string]any{}
	var stderr []byte
	if strings.TrimSpace(cmdStr) != "" {
		stdout, capturedStderr, runErr := execForNode(r.Execution, firstExecutor(envExecutor), cmdStr, session.WorktreePath)
		stderr = capturedStderr
		if runErr != nil {
			return nil, stderr, fmt.Errorf("setup: %w", runErr)
		}
		parsed, parseErr := ParseOutputs(stdout)
		if parseErr != nil {
			return nil, stderr, fmt.Errorf("setup: %w", parseErr)
		}
		outputs = parsed
	}
	if r.OutputsSchema != nil {
		if vErr := r.OutputsSchema.Validate(outputs); vErr != nil {
			return nil, stderr, fmt.Errorf("setup: outputs schema: %w", vErr)
		}
	}
	return outputs, stderr, nil
}
