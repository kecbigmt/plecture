package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SchemaProperties returns the schema document's `properties` map (inline map
// or schema file — the two are mutually exclusive), or nil when neither source
// is set or the document declares no properties. Shared by the task package's
// mutable-output policy and the config package's own chain input-binding
// validation, so both read the same JSON Schema document the same way.
func SchemaProperties(inline map[string]any, filePath string) (map[string]any, error) {
	raw, err := loadRawSchema(inline, filePath)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	props, _ := raw["properties"].(map[string]any)
	if len(props) == 0 {
		return nil, nil
	}
	return props, nil
}

// SchemaPropertyNames returns the declared `properties` keys of a schema
// (inline map or schema file), sorted. Returns nil when the schema declares no
// properties.
func SchemaPropertyNames(inline map[string]any, filePath string) ([]string, error) {
	props, err := SchemaProperties(inline, filePath)
	if err != nil {
		return nil, err
	}
	if len(props) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// loadRawSchema returns the schema document as a raw map, reading filePath
// when the schema is file-based. Returns nil when neither source is set.
func loadRawSchema(inline map[string]any, filePath string) (map[string]any, error) {
	hasInline := len(inline) > 0
	hasFile := filePath != ""
	if hasInline && hasFile {
		return nil, fmt.Errorf("inline schema and schema file are mutually exclusive")
	}
	switch {
	case hasInline:
		return inline, nil
	case hasFile:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filePath, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", filePath, err)
		}
		return m, nil
	}
	return nil, nil
}
