package lang

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ParseOutputs parses an effect setup's stdout as JSON. Empty input is
// treated as an empty object. Parse failure returns an error so the caller
// can mark the task as failed (contract violation).
//
// The contract requires a JSON *object*. A literal `null` unmarshals into a
// nil map without error, so we reject it explicitly — silently treating it
// as `{}` would mask a misbehaving setup script.
func ParseOutputs(stdout []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, fmt.Errorf("setup stdout is not a JSON object: %w", err)
	}
	if out == nil {
		return nil, fmt.Errorf("setup stdout is not a JSON object: got null")
	}
	return out, nil
}
