package task

import (
	"fmt"
	"sort"

	"github.com/plecture/plect/app/internal/config"
	contract "github.com/plecture/plect/contracts/state"
)

// MutableOutputKeys extracts the output keys declared `mutable = true` from an
// outputs schema (inline map or schema file — same precedence rules as
// CompileSchema). `mutable` is a custom annotation keyword: JSON Schema
// validators ignore unknown keywords, so the same document drives both
// validation and the set-output write policy.
//
// Declaring the reserved `workdir` key mutable is a load error — cleanup
// correctness depends on workdir, so no external updater may ever rewrite it.
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
			if name == contract.OutputKeyWorkdir {
				return nil, fmt.Errorf("output key %q is reserved and always immutable; remove `mutable = true`", contract.OutputKeyWorkdir)
			}
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
