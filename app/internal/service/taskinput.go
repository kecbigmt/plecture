package service

import (
	"fmt"
	"maps"
)

// MergeTaskInput folds the --task/task shorthand into a session inputs map.
// It is pure syntax sugar over {"task": taskID} — the core has no opinion on
// what task ids are valid; that is enforced by the active workflow's
// inputs_schema at create time, same as any other input.
func MergeTaskInput(inputs map[string]any, taskID string) (map[string]any, error) {
	if taskID == "" {
		return inputs, nil
	}
	merged := make(map[string]any, len(inputs)+1)
	maps.Copy(merged, inputs)
	if existing, ok := merged["task"]; ok && existing != taskID {
		return nil, fmt.Errorf("--task %q conflicts with \"task\": %v already set via --inputs/--inputs-file", taskID, existing)
	}
	merged["task"] = taskID
	return merged, nil
}
