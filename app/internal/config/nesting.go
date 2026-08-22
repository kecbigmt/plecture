package config

import (
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// OutputKeyInteractiveEndpoint is the public output a nesting chain's
// terminal-declaring layer projects into the composed public contract, so
// attach/capture/send reach that layer through the contract rather than
// through nesting-aware lookup.
const OutputKeyInteractiveEndpoint = "interactive_endpoint"

// IsNested reports whether this definition is the outer layer of a nesting
// chain.
func (d TaskDefinition) IsNested() bool {
	return strings.TrimSpace(d.Inner) != ""
}

// ResolvedLocalsSchemaPath joins LocalsSchemaFile with BaseDir.
func (d TaskDefinition) ResolvedLocalsSchemaPath() string {
	return resolveSchemaPath(d.LocalsSchemaFile, d.BaseDir)
}

// envNameRE is the POSIX-portable process environment name charset. An
// `inner.env` key outside it could never be exported to the inner effect's
// executions, so it is rejected where it is written rather than swallowed by
// the shell at run time.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// OutputBinding is one `[outputs.bind]` entry after classification.
//
// A direct binding projects the inner output's native value and routes
// mutable writes to it; a computed one produces a new value. The two carry
// different schema obligations and different runtime behavior, so which one
// an entry is has to be decided statically, from the value alone.
type OutputBinding struct {
	// Key is the public output name this entry defines.
	Key string
	// Value is the entry as declared, which the projection resolves.
	Value *lang.Value
	// Direct is true when the value is exactly one projection of an inner
	// public output.
	Direct bool
	// InnerKey is the bound inner output for a direct binding.
	InnerKey string
	// InnerRefs lists every inner output the value reads, direct or not.
	InnerRefs []string
}

const innerOutputRoot = "inner.outputs."

// innerOutputRefRE finds the inner outputs a computation reads. A
// computation is CEL rather than a template, so the reference is the same
// dotted path a projection writes.
var innerOutputRefRE = regexp.MustCompile(`\binner\.outputs\.([A-Za-z_][A-Za-z0-9_]*)`)

// ClassifyOutputBinding decides whether value is a direct projection of one
// inner output, and collects every inner output it reads.
func ClassifyOutputBinding(key string, value *lang.Value) OutputBinding {
	b := OutputBinding{Key: key, Value: value}
	seen := map[string]bool{}
	for _, leaf := range lang.ValueLeaves(value) {
		switch leaf.Form {
		case lang.FormFrom:
			inner, ok := strings.CutPrefix(leaf.From, innerOutputRoot)
			if !ok {
				continue
			}
			if leaf == value {
				b.Direct = true
				b.InnerKey = inner
			}
			if !seen[inner] {
				seen[inner] = true
				b.InnerRefs = append(b.InnerRefs, inner)
			}
		case lang.FormExpr:
			for _, match := range innerOutputRefRE.FindAllStringSubmatch(leaf.Expr, -1) {
				if !seen[match[1]] {
					seen[match[1]] = true
					b.InnerRefs = append(b.InnerRefs, match[1])
				}
			}
		}
	}
	sort.Strings(b.InnerRefs)
	return b
}

// schemaPropertyType returns a property's declared `type` when it is a plain
// string. A union type (array form) or an absent one leaves nothing for the
// binding rules to disagree with, so both read as undeclared.
func schemaPropertyType(props map[string]any, key string) (string, bool) {
	prop, ok := props[key].(map[string]any)
	if !ok {
		return "", false
	}
	t, ok := prop["type"].(string)
	return t, ok
}

// schemaPropertyMutable reports the property's `mutable` annotation.
func schemaPropertyMutable(props map[string]any, key string) bool {
	prop, ok := props[key].(map[string]any)
	if !ok {
		return false
	}
	b, _ := prop["mutable"].(bool)
	return b
}

// ClassifiedOutputBindings returns this layer's `[outputs.bind]` entries in
// a stable key order, each classified as direct or computed. Load-time
// validation and the runtime projection read the same classification from
// here rather than each deciding it again.
func (d TaskDefinition) ClassifiedOutputBindings() []OutputBinding {
	keys := make([]string, 0, len(d.OutputsBind))
	for k := range d.OutputsBind {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]OutputBinding, 0, len(keys))
	for _, k := range keys {
		out = append(out, ClassifyOutputBinding(k, d.OutputsBind[k]))
	}
	return out
}

// SchemaRequiredNames returns the schema document's `required` list, and
// SchemaIsClosed reports whether it sets `additionalProperties = false`.
// Together they are what `bind.inputs` has to satisfy for the inner task:
// every required input bound, and nothing bound that a closed schema rejects.
func SchemaRequiredNames(inline map[string]any, filePath string) ([]string, error) {
	raw, err := loadRawSchema(inline, filePath)
	if err != nil || raw == nil {
		return nil, err
	}
	items, _ := raw["required"].([]any)
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func SchemaIsClosed(inline map[string]any, filePath string) (bool, error) {
	raw, err := loadRawSchema(inline, filePath)
	if err != nil || raw == nil {
		return false, err
	}
	allowed, ok := raw["additionalProperties"].(bool)
	return ok && !allowed, nil
}
