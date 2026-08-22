package lang

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidateProviderContracts checks the two provider rules that read one
// declaration against another part of the same declaration: a session name is
// built from captures its own regular expression declares, and a cleanup
// action reads outputs its own contract declares. Both are
// PLECTURE-CFG-FROM-PATH — the root exists, the key inside it does not.
//
// Neither can be a surface check, because a surface knows which roots a site
// observes and nothing about what this provider's regular expression captures
// or its schema declares.
func (v Validation) ValidateProviderContracts(def *Definition) error {
	if def.Kind != KindWorkspaceProvider {
		return nil
	}
	pos := Position{File: def.File, Path: def.ID}
	captures, err := matchCaptures(def, pos)
	if err != nil {
		return err
	}
	if name, ok := def.Body["name"]; ok {
		value, err := ParseValue(name, ClassData, childPos(pos, "name"))
		if err != nil {
			return err
		}
		for _, path := range valuePaths(value, surfaceProviderName) {
			if err := resolveCapture(path, captures, childPos(pos, "name")); err != nil {
				return err
			}
		}
	}
	if _, ok := def.Body["cleanup"]; ok {
		action, err := ParseAction(def.Body["cleanup"], childPos(pos, "cleanup"))
		if err != nil {
			return err
		}
		outputs := declaredProperties(schemaTable(def.Body["outputs_schema"]))
		for _, value := range action.values() {
			for _, path := range valuePaths(value, surfaceProviderCleanup) {
				if err := resolveOutput(path, outputs, def.Body["outputs_schema"] != nil, childPos(pos, "cleanup")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// matchCaptures reads the names a provider's regular expression captures. An
// uncompilable expression yields none rather than an error of its own: the
// loader reports that, and this pass has nothing to add.
func matchCaptures(def *Definition, pos Position) (map[string]bool, error) {
	raw, ok := def.Body["match"]
	if !ok {
		return nil, nil
	}
	pattern, ok := raw.(string)
	if !ok {
		return nil, newDiag(CodeFieldType, LayerStructural, childPos(pos, "match"),
			"match is a regular expression string")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, nil
	}
	captures := map[string]bool{}
	for _, name := range re.SubexpNames() {
		if name != "" {
			captures[name] = true
		}
	}
	return captures, nil
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
		fmt.Sprintf("%q names no capture this provider's match declares", path))
}

// resolveOutput checks one cleanup projection against the provider's outputs
// contract. A provider declaring no contract at all is left alone: there is
// nothing to check the key against, and demanding a schema is a different
// rule than this one.
func resolveOutput(path string, outputs map[string]bool, declared bool, pos Position) error {
	segments := strings.Split(path, ".")
	if len(segments) < 3 || segments[0] != "self" || segments[1] != "outputs" {
		return nil
	}
	if !declared || outputs[segments[2]] {
		return nil
	}
	return newDiag(CodeFromPath, LayerSemantic, pos,
		fmt.Sprintf("%q names no property this provider's outputs_schema declares", path))
}

func schemaTable(raw any) map[string]any {
	table, _ := raw.(map[string]any)
	return table
}
