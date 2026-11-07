package commands

import (
	"encoding/json"
	"fmt"
	"os"
)

// resolveInputsFlags returns (nil, nil) when neither flag is set so the
// service can distinguish "no inputs" from "empty object".
func resolveInputsFlags(inputsJSON, inputsFile string) (map[string]any, error) {
	return resolveObjectFlags("inputs", inputsJSON, inputsFile)
}

// resolveObjectFlags reads one JSON object from the --<name> / --<name>-file
// flag pair.
func resolveObjectFlags(name, inline, file string) (map[string]any, error) {
	if inline != "" && file != "" {
		return nil, fmt.Errorf("--%[1]s and --%[1]s-file are mutually exclusive", name)
	}
	var raw []byte
	switch {
	case inline != "":
		raw = []byte(inline)
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read --%s-file %s: %w", name, file, err)
		}
		raw = data
	default:
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s as JSON object: %w", name, err)
	}
	if out == nil {
		return nil, fmt.Errorf("%s must be a JSON object, got null", name)
	}
	return out, nil
}
