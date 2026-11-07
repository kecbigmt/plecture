package task

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// cleanupContract is one effect's declaration of the destruction inputs it
// reads: the schema, and the effect it came from for diagnostics.
type cleanupContract struct {
	effect string
	schema map[string]any
}

func (r Resolved) cleanupContracts() []cleanupContract {
	var out []cleanupContract
	if r.CleanupInputsSchema != nil {
		out = append(out, cleanupContract{effect: r.TaskID, schema: r.CleanupInputsSchema})
	}
	for _, layer := range r.Layers {
		if layer.CleanupInputsSchema != nil {
			out = append(out, cleanupContract{effect: layer.EffectID, schema: layer.CleanupInputsSchema})
		}
	}
	return out
}

func schemaProperties(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	return props
}

// FilterCleanupInputs is the subset of an invocation's cleanup inputs one
// effect declared. An effect with no cleanup input schema receives none, so
// its `default` fires instead of reading a key some other effect owns.
func FilterCleanupInputs(schema map[string]any, all map[string]any) map[string]any {
	if schema == nil || len(all) == 0 {
		return nil
	}
	props := schemaProperties(schema)
	out := make(map[string]any)
	for key, value := range all {
		if _, declared := props[key]; declared {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ResolveCleanupInputs checks an invocation's cleanup inputs against every
// cleanup contract the effects being released declare, and returns them typed
// by those contracts. A key no effect declares is an error, because the caller
// would otherwise believe an intent was honored; a key two effects both declare
// must carry the same property schema, because a single value is supplied to
// both.
func ResolveCleanupInputs(nodes []Resolved, invocation map[string]string) (map[string]any, error) {
	if len(invocation) == 0 {
		return nil, nil
	}
	declared := make(map[string]map[string]any)
	owners := make(map[string][]string)
	var contracts []cleanupContract
	for _, r := range nodes {
		contracts = append(contracts, r.cleanupContracts()...)
	}
	for _, c := range contracts {
		for key, prop := range schemaProperties(c.schema) {
			schema, _ := prop.(map[string]any)
			if prior, seen := declared[key]; seen && !reflect.DeepEqual(prior, schema) {
				if _, supplied := invocation[key]; supplied {
					return nil, fmt.Errorf("cleanup input %q is declared with different schemas by effects %q and %q", key, owners[key][0], c.effect)
				}
			}
			declared[key] = schema
			owners[key] = append(owners[key], c.effect)
		}
	}
	keys := make([]string, 0, len(invocation))
	for key := range invocation {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	typed := make(map[string]any, len(invocation))
	for _, key := range keys {
		schema, ok := declared[key]
		if !ok {
			return nil, fmt.Errorf("cleanup input %q is not declared by any effect being released", key)
		}
		value, err := coerceCleanupInput(invocation[key], schema)
		if err != nil {
			return nil, fmt.Errorf("cleanup input %q: %w", key, err)
		}
		typed[key] = value
	}
	for _, c := range contracts {
		supplied := FilterCleanupInputs(c.schema, typed)
		if supplied == nil {
			continue
		}
		compiled, err := lang.CompileSchema(c.schema, "", "plect:cleanup:"+c.effect)
		if err != nil {
			return nil, fmt.Errorf("effect %q cleanup inputs schema: %w", c.effect, err)
		}
		if err := compiled.Validate(supplied); err != nil {
			return nil, fmt.Errorf("effect %q cleanup inputs: %w", c.effect, err)
		}
	}
	return typed, nil
}

// coerceCleanupInput types a command-line string by its property's declared
// type, since the invocation surface carries only strings.
func coerceCleanupInput(raw string, schema map[string]any) (any, error) {
	switch schema["type"] {
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean", raw)
		}
		return v, nil
	case "integer":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		return float64(v), nil
	case "number":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return v, nil
	}
	return raw, nil
}
