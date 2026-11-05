package task

import (
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// MutableOutputKeys extracts the output keys declared `mutable = true` from an
// outputs schema (inline map or schema file — same precedence rules as
// CompileSchema). `mutable` is a custom annotation keyword: JSON Schema
// validators ignore unknown keywords, so the same document drives both
// validation and the set-output write policy.
func MutableOutputKeys(inline map[string]any, filePath string) ([]string, error) {
	props, err := config.SchemaProperties(inline, filePath)
	if err != nil {
		return nil, err
	}
	var keys []string
	for name, prop := range props {
		m, ok := prop.(map[string]any)
		if !ok {
			continue
		}
		if b, ok := m["mutable"].(bool); ok && b {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// SchemaPropertyNames returns the declared `properties` keys of a schema
// (inline map or schema file), sorted. Used by the dynamic `plect task setup`
// path to know which inputs to bind from the --input / outputs / session
// precedence. Returns nil when the schema declares no properties.
func SchemaPropertyNames(inline map[string]any, filePath string) ([]string, error) {
	return config.SchemaPropertyNames(inline, filePath)
}
