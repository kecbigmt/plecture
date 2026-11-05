package lang

import (
	"fmt"
	"regexp"
	"strings"
)

// nameContract checks that a `name` value reads only captures the same
// declaration's `match` declares. A resource derives its name from one identifier before any session
// exists, so the name reads only the captures its own `match` declares.
func nameContract(def *Definition, pos Position, surface *Surface) error {
	captures, known, err := matchCaptures(def, pos)
	if err != nil {
		return err
	}
	name, ok := def.Body["name"]
	if !ok || !known {
		return nil
	}
	value, err := ParseValue(name, ClassData, childPos(pos, "name"))
	if err != nil {
		return err
	}
	for _, path := range valuePaths(value, surface) {
		if err := resolveCapture(path, captures, childPos(pos, "name")); err != nil {
			return err
		}
	}
	return nil
}

// matchCaptures reads the names a provider's regular expression captures.
// known is false for an expression that does not compile: reporting every
// capture reference as unresolvable would bury the reason none of them can
// be resolved.
func matchCaptures(def *Definition, pos Position) (captures map[string]bool, known bool, err error) {
	raw, ok := def.Body["match"]
	if !ok {
		return nil, true, nil
	}
	pattern, ok := raw.(string)
	if !ok {
		return nil, false, newDiag(CodeFieldType, LayerStructural, childPos(pos, "match"),
			"match is a regular expression string")
	}
	re, compileErr := regexp.Compile(pattern)
	if compileErr != nil {
		return nil, false, newDiag(CodeFieldType, LayerStructural, childPos(pos, "match"),
			fmt.Sprintf("%q does not compile as a regular expression: %s", pattern, oneLine(compileErr)))
	}
	captures = map[string]bool{}
	for _, name := range re.SubexpNames() {
		if name != "" {
			captures[name] = true
		}
	}
	return captures, true, nil
}

// valuePaths yields every root path one value reads, whether it projects one
// directly or computes over several.
func valuePaths(value *Value, s *Surface) []string {
	switch value.Form {
	case FormFrom:
		return []string{value.From}
	case FormExpr:
		return expressionPaths(value.Expr, s)
	case FormJSON:
		return operandPaths(value.JSON, s)
	default:
		return nil
	}
}

func operandPaths(op *JSONOperand, s *Surface) []string {
	switch {
	case op == nil:
		return nil
	case op.Leaf != nil:
		return valuePaths(op.Leaf, s)
	case op.Object != nil:
		var paths []string
		for _, key := range sortedOperandKeys(op.Object) {
			paths = append(paths, operandPaths(op.Object[key], s)...)
		}
		return paths
	default:
		var paths []string
		for _, child := range op.Array {
			paths = append(paths, operandPaths(child, s)...)
		}
		return paths
	}
}

func resolveCapture(path string, captures map[string]bool, pos Position) error {
	segments := strings.Split(path, ".")
	if len(segments) < 2 || segments[0] != "match" {
		return nil
	}
	if captures[segments[1]] {
		return nil
	}
	return newDiag(CodeFromPath, LayerSemantic, pos,
		fmt.Sprintf("%q names no capture this resource's match declares", path))
}
