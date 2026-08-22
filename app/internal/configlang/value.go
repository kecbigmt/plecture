package configlang

import (
	"fmt"
	"sort"
	"strings"
)

type ValueForm string

const (
	FormLiteral  ValueForm = "literal"
	FormFrom     ValueForm = "from"
	FormExpr     ValueForm = "expr"
	FormTerminal ValueForm = "terminal"
	FormBin      ValueForm = "bin"
	FormJSON     ValueForm = "json"
)

// ValueClass says which forms one location accepts. The three classes are
// the schema's three value surfaces: a workflow's workspace-provider
// parameters take literal data, most surfaces consume data, and only an
// action binding and an action's argv can consume a Plecture capability.
type ValueClass int

const (
	ClassLiteral ValueClass = iota
	ClassData
	ClassBinding
)

// Value carries exactly one form's fields populated, named by Form.
type Value struct {
	Form ValueForm

	Literal any

	From       string
	Default    any
	HasDefault bool
	Optional   bool

	Expr string

	Terminal string
	Bin      string

	JSON *JSONOperand

	Pos Position
}

type JSONOperand struct {
	Leaf   *Value
	Object map[string]*JSONOperand
	Array  []*JSONOperand
}

// tagKeys are the vocabulary's discriminators. `default` and `optional`
// belong to a tagged value but name no form on their own, so they are absent
// here: one of these keys alone is what identifies a table as a tagged value,
// both at a value surface and inside a contract document.
var tagKeys = []string{"from", "expr", "terminal", "bin", "json"}

// terminalVerbOrder is ordered so a walk over a terminal table reports the
// same diagnostic on every run.
var terminalVerbOrder = []string{"attach", "capture", "send_text", "send_keys"}

var terminalVerbs = func() map[string]bool {
	verbs := make(map[string]bool, len(terminalVerbOrder))
	for _, verb := range terminalVerbOrder {
		verbs[verb] = true
	}
	return verbs
}()

// ParseValue reads one value at a location accepting class, applying
// values.md's validation rules for each form and their composition. A
// non-table is a literal without further checks: only a table can carry a
// tag, so only a table can carry a tag on the wrong surface.
func ParseValue(raw any, class ValueClass, pos Position) (*Value, error) {
	tbl, isTable := raw.(map[string]any)
	if !isTable {
		return &Value{Form: FormLiteral, Literal: raw, Pos: pos}, nil
	}
	if class == ClassLiteral {
		return nil, newDiag(CodeValueTagSurface, LayerStructural, pos,
			"this surface accepts literal data only")
	}
	return parseTagged(tbl, class, pos)
}

func parseTagged(tbl map[string]any, class ValueClass, pos Position) (*Value, error) {
	var present []string
	for _, k := range tagKeys {
		if _, ok := tbl[k]; ok {
			present = append(present, k)
		}
	}
	if _, from := tbl["from"]; from {
		if _, expr := tbl["expr"]; expr {
			return nil, newDiag(CodeValueFromAndExpr, LayerStructural, pos,
				"a value declaring both from and expr has no unambiguous form")
		}
	}
	if len(present) != 1 {
		return nil, newDiag(CodeValueTagUnknown, LayerStructural, pos,
			fmt.Sprintf("keys %s match no entry of the tagged-value vocabulary", quotedKeys(tbl)))
	}

	switch present[0] {
	case "from":
		return parseFrom(tbl, pos)
	case "expr":
		if err := onlyKeys(tbl, pos, "expr"); err != nil {
			return nil, err
		}
		expr, err := tagString(tbl, "expr", pos)
		if err != nil {
			return nil, err
		}
		return &Value{Form: FormExpr, Expr: expr, Pos: pos}, nil
	case "terminal":
		if class != ClassBinding {
			return nil, newDiag(CodeValueTagSurface, LayerStructural, pos,
				"a terminal capability is consumed by an action binding or an action's argv, not by a surface that consumes data only")
		}
		if err := onlyKeys(tbl, pos, "terminal"); err != nil {
			return nil, err
		}
		verb, err := tagString(tbl, "terminal", pos)
		if err != nil {
			return nil, err
		}
		if !terminalVerbs[verb] {
			return nil, newDiag(CodeValueTagUnknown, LayerStructural, pos,
				fmt.Sprintf("%q is not one of the terminal verbs attach, capture, send_text, send_keys", verb))
		}
		return &Value{Form: FormTerminal, Terminal: verb, Pos: pos}, nil
	case "bin":
		if class != ClassBinding {
			return nil, newDiag(CodeValueTagSurface, LayerStructural, pos,
				"a plugin-owned executable is consumed by an action binding or an action's argv, not by a surface that consumes data only")
		}
		if err := onlyKeys(tbl, pos, "bin"); err != nil {
			return nil, err
		}
		name, err := tagString(tbl, "bin", pos)
		if err != nil {
			return nil, err
		}
		return &Value{Form: FormBin, Bin: name, Pos: pos}, nil
	default:
		if err := onlyKeys(tbl, pos, "json"); err != nil {
			return nil, err
		}
		operand, err := parseJSONOperand(tbl["json"], pos)
		if err != nil {
			return nil, err
		}
		return &Value{Form: FormJSON, JSON: operand, Pos: pos}, nil
	}
}

func parseFrom(tbl map[string]any, pos Position) (*Value, error) {
	if err := onlyKeys(tbl, pos, "from", "default", "optional"); err != nil {
		return nil, err
	}
	path, err := tagString(tbl, "from", pos)
	if err != nil {
		return nil, err
	}
	v := &Value{Form: FormFrom, From: path, Pos: pos}
	def, hasDefault := tbl["default"]
	opt, hasOptional := tbl["optional"]
	if hasDefault && hasOptional {
		return nil, newDiag(CodeValueDefaultAndOptional, LayerStructural, pos,
			"default supplies a value and optional propagates absence; a value declares at most one")
	}
	if hasDefault {
		v.Default, v.HasDefault = def, true
	}
	if hasOptional {
		if opt != true {
			return nil, newDiag(CodeFieldType, LayerStructural, pos, "optional is true or absent")
		}
		v.Optional = true
	}
	return v, nil
}

// parseJSONOperand reads a `{ json = ... }` operand. The serializer is a
// boundary, so a capability tag has no meaning inside it and a nested json
// tag would serialize twice.
func parseJSONOperand(raw any, pos Position) (*JSONOperand, error) {
	switch v := raw.(type) {
	case map[string]any:
		for _, k := range tagKeys {
			if _, ok := v[k]; !ok {
				continue
			}
			leaf, err := parseTagged(v, ClassData, pos)
			if err != nil {
				return nil, err
			}
			if leaf.Form == FormJSON {
				return nil, newDiag(CodeValueTagUnknown, LayerStructural, pos,
					"a json operand's leaves are literals, projections, or computations")
			}
			return &JSONOperand{Leaf: leaf}, nil
		}
		obj := make(map[string]*JSONOperand, len(v))
		for key, field := range v {
			child, err := parseJSONOperand(field, childPos(pos, key))
			if err != nil {
				return nil, err
			}
			obj[key] = child
		}
		return &JSONOperand{Object: obj}, nil
	case []any:
		arr := make([]*JSONOperand, 0, len(v))
		for i, item := range v {
			child, err := parseJSONOperand(item, childPos(pos, fmt.Sprintf("[%d]", i)))
			if err != nil {
				return nil, err
			}
			arr = append(arr, child)
		}
		return &JSONOperand{Array: arr}, nil
	default:
		return &JSONOperand{Leaf: &Value{Form: FormLiteral, Literal: raw, Pos: pos}}, nil
	}
}

// checkNoTaggedValues rejects a tagged value anywhere inside a contract
// document: a contract declares types, not values.
func checkNoTaggedValues(raw any, pos Position) error {
	switch v := raw.(type) {
	case map[string]any:
		for _, k := range tagKeys {
			if _, ok := v[k]; ok {
				return newDiag(CodeValueTagSurface, LayerStructural, pos,
					fmt.Sprintf("a contract document declares types, not values, so the tagged value %s does not belong here", quotedKeys(v)))
			}
		}
		for key, field := range v {
			if err := checkNoTaggedValues(field, childPos(pos, key)); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range v {
			if err := checkNoTaggedValues(item, childPos(pos, fmt.Sprintf("[%d]", i))); err != nil {
				return err
			}
		}
	}
	return nil
}

func onlyKeys(tbl map[string]any, pos Position, allowed ...string) error {
	ok := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		ok[k] = true
	}
	for k := range tbl {
		if !ok[k] {
			return newDiag(CodeValueTagUnknown, LayerStructural, pos,
				fmt.Sprintf("%q is not part of the { %s } form", k, strings.Join(allowed, ", ")))
		}
	}
	return nil
}

func tagString(tbl map[string]any, key string, pos Position) (string, error) {
	s, ok := tbl[key].(string)
	if !ok {
		return "", newDiag(CodeFieldType, LayerStructural, pos, fmt.Sprintf("%s takes a string", key))
	}
	return s, nil
}

// sortedKeys orders a table's keys so a walk over it reports the same
// diagnostic on every run.
func sortedKeys(tbl map[string]any) []string {
	keys := make([]string, 0, len(tbl))
	for k := range tbl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func quotedKeys(tbl map[string]any) string {
	keys := make([]string, 0, len(tbl))
	for k := range tbl {
		keys = append(keys, fmt.Sprintf("%q", k))
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func childPos(pos Position, segment string) Position {
	if strings.HasPrefix(segment, "[") {
		return Position{File: pos.File, Path: pos.Path + segment}
	}
	if pos.Path == "" {
		return Position{File: pos.File, Path: segment}
	}
	return Position{File: pos.File, Path: pos.Path + "." + segment}
}
