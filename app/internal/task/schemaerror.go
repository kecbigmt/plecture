package task

import (
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// DescribeValidationError renders a jsonschema validation error and appends
// the enum candidates for any property that failed via a missing-required or
// mismatched-enum violation, so a caller sees the valid choices instead of a
// bare "missing property" message. It knows nothing about any specific
// property's name or meaning — the hint comes entirely from what the schema
// itself declares, so it applies uniformly to any inputs_schema.
func DescribeValidationError(schema *jsonschema.Schema, err error) string {
	msg := err.Error()
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return msg
	}
	hints := collectEnumHints(schema, ve, nil)
	if len(hints) == 0 {
		return msg
	}
	return msg + "\n" + strings.Join(hints, "\n")
}

func collectEnumHints(root *jsonschema.Schema, e *jsonschema.ValidationError, hints []string) []string {
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		containing := schemaAtLocation(root, e.InstanceLocation)
		for _, name := range k.Missing {
			if prop := propertySchema(containing, name); prop != nil && prop.Enum != nil {
				hints = appendHint(hints, propertyPath(e.InstanceLocation, name), prop.Enum.Values)
			}
		}
	case *kind.Enum:
		hints = appendHint(hints, strings.Join(e.InstanceLocation, "."), k.Want)
	}
	for _, cause := range e.Causes {
		hints = collectEnumHints(root, cause, hints)
	}
	return hints
}

func appendHint(hints []string, path string, choices []any) []string {
	hint := fmt.Sprintf("%q: valid choices are %s", path, joinValues(choices))
	if slices.Contains(hints, hint) {
		return hints
	}
	return append(hints, hint)
}

func propertyPath(location []string, name string) string {
	if len(location) == 0 {
		return name
	}
	return strings.Join(location, ".") + "." + name
}

func joinValues(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return strings.Join(parts, ", ")
}

// schemaAtLocation walks root's property schemas by instance-location
// segments, unwrapping allOf branches at each step (combineInputsSchemas
// wraps cascaded layers in allOf, so a cascade's schema isn't a flat
// Properties map at the top level).
func schemaAtLocation(root *jsonschema.Schema, location []string) *jsonschema.Schema {
	cur := root
	for _, seg := range location {
		cur = propertySchema(cur, seg)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// propertySchema finds the schema declared for a property name anywhere in
// s's Properties or (recursively) its allOf branches.
func propertySchema(s *jsonschema.Schema, name string) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	if prop, ok := s.Properties[name]; ok {
		return prop
	}
	for _, sub := range s.AllOf {
		if prop := propertySchema(sub, name); prop != nil {
			return prop
		}
	}
	return nil
}
