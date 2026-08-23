package lang

import (
	"fmt"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Kind is one of the definition-block kinds the language vocabulary
// declares.
type Kind string

const (
	KindEffect            Kind = "effect"
	KindTask              Kind = "task"
	KindChannel           Kind = "channel"
	KindWorkflow          Kind = "workflow"
	KindWorkspaceProvider Kind = "workspace_provider"
	KindResourceObserver  Kind = "resource_observer"
)

var validKinds = map[Kind]bool{
	KindEffect:            true,
	KindTask:              true,
	KindChannel:           true,
	KindWorkflow:          true,
	KindWorkspaceProvider: true,
	KindResourceObserver:  true,
}

// idPattern is the definition id grammar: a TOML bare-key segment excluding
// dots (address separators) and hyphens (an effect id doubles as a workflow
// node id, which must be a safe dotted path segment). BurntSushi/toml decodes
// a bare and a quoted key to the same Go map key, so this check cannot tell
// them apart — "quoted keys are not ids" is not enforced here.
var idPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isValidID(id string) bool {
	return idPattern.MatchString(id)
}

// asTableArray normalizes an array-of-tables field to []map[string]any.
// BurntSushi/toml decodes such a field as []map[string]interface{} when
// decoding into a generic map, but a value built by hand (as in a test, or
// after mergeCascade's own append) may already be a plain []interface{}
// holding map[string]any elements; this accepts either.
func asTableArray(v any) ([]map[string]any, bool) {
	switch arr := v.(type) {
	case []map[string]any:
		return arr, true
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	}
	return nil, false
}

// Definition is one discovered `[<id>] kind = "<kind>"` table, whatever file
// it came from. Body holds every field of the table except id and kind, with
// nested tables and arrays exactly as TOML decoded them. Instruction is a
// task's `instructions` array elements resolved and joined, empty for every
// other kind and for a task that declares none.
type Definition struct {
	ID          string
	Kind        Kind
	Body        map[string]any
	Instruction string
	File        string
}

// ParseDefinitionDocument decodes a TOML definition document (one that is
// not a reserved root file) into its top-level definitions, applying the
// structural rules that gate whether each block is even a definition: `kind`
// present and in the vocabulary, and the id well-formed.
func ParseDefinitionDocument(path string, src []byte) ([]*Definition, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(src), &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s: no top-level definition table", path)
	}
	defs := make([]*Definition, 0, len(raw))
	for id, v := range raw {
		tbl, ok := v.(map[string]any)
		if !ok {
			return nil, newDiag(CodeKindMissing, LayerStructural, Position{File: path, Path: id},
				fmt.Sprintf("%q is not a definition table", id))
		}
		def, err := parseDefinitionTable(path, id, tbl)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// parseDefinitionTable applies the structural rules common to every
// definition table, regardless of which file kind it was found in: kind
// present and in the vocabulary, then the id well-formed.
func parseDefinitionTable(path, id string, tbl map[string]any) (*Definition, error) {
	kindVal, ok := tbl["kind"]
	if !ok {
		return nil, newDiag(CodeKindMissing, LayerStructural, Position{File: path, Path: id},
			fmt.Sprintf("definition %q declares no kind", id))
	}
	kindStr, ok := kindVal.(string)
	if !ok || !validKinds[Kind(kindStr)] {
		return nil, newDiag(CodeKindUnknown, LayerStructural, Position{File: path, Path: id + ".kind"},
			fmt.Sprintf("definition %q declares kind %v, outside the vocabulary", id, kindVal))
	}
	if !isValidID(id) {
		return nil, newDiag(CodeIDInvalid, LayerStructural, Position{File: path, Path: id},
			fmt.Sprintf("id %q does not match ^[A-Za-z_][A-Za-z0-9_]*$", id))
	}
	body := make(map[string]any, len(tbl))
	for k, v := range tbl {
		if k == "kind" {
			continue
		}
		body[k] = v
	}
	return &Definition{ID: id, Kind: Kind(kindStr), Body: body, File: path}, nil
}
