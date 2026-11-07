package commands

import (
	"encoding/json"
	"fmt"
	"os"
)

// resolveInputsFlags returns (nil, nil) when neither flag is set so the
// service can distinguish "no inputs" from "empty object".
func resolveInputsFlags(inputsJSON, inputsFile string) (map[string]any, error) {
	if inputsJSON != "" && inputsFile != "" {
		return nil, fmt.Errorf("--inputs and --inputs-file are mutually exclusive")
	}
	var raw []byte
	switch {
	case inputsJSON != "":
		raw = []byte(inputsJSON)
	case inputsFile != "":
		data, err := os.ReadFile(inputsFile)
		if err != nil {
			return nil, fmt.Errorf("read --inputs-file %s: %w", inputsFile, err)
		}
		raw = data
	default:
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse inputs as JSON object: %w", err)
	}
	if out == nil {
		return nil, fmt.Errorf("inputs must be a JSON object, got null")
	}
	return out, nil
}
