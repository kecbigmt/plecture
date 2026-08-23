package lang

import (
	"fmt"
	"reflect"
)

// ExtendsChain resolves the chain of task declarations def's `extends`
// reaches, ordered innermost first: chain[0] is the root declaration that
// itself declares no `extends`, and chain[len(chain)-1] is def's immediate
// base. An empty chain means def declares no extends of its own.
//
// Ownership resolves relative to whichever declaration wrote each `extends`
// reference, mirroring effect nesting's per-layer namespace rule: a
// catalog-qualified base's own extends resolves in that base's namespace,
// not the referencing declaration's.
func (v Validation) ExtendsChain(def *Definition, r *Registry) ([]*Definition, error) {
	var outward []*Definition // def's immediate base first, root last
	seen := map[*Definition]bool{def: true}
	cur := def
	curFrom := v.From
	for {
		raw, ok := cur.Body["extends"]
		if !ok {
			break
		}
		at := childPos(Position{File: cur.File, Path: cur.ID}, "extends")
		ref, err := staticRef(raw, at.Path)
		if err != nil {
			return nil, err
		}
		base, err := r.ExpectKind(ref, curFrom, KindTask, at.Path)
		if err != nil {
			return nil, err
		}
		if seen[base] {
			return nil, newDiag(CodeExtendsCycle, LayerSemantic, at,
				fmt.Sprintf("extends %q forms a cycle", ref))
		}
		seen[base] = true
		outward = append(outward, base)
		cur = base
		curFrom = r.OwnerOf(base)
	}
	chain := make([]*Definition, len(outward))
	for i, d := range outward {
		chain[len(outward)-1-i] = d
	}
	return chain, nil
}

// composedJudges accumulates judge ids across an extends chain, root layer
// first, catching the one collision the whole chain must never allow: two
// declarations answering the same judge id would let a later layer silently
// widen or narrow what an earlier layer already committed to.
func composedJudges(layers []*Definition) (map[string]bool, error) {
	owner := map[string]string{}
	for _, layer := range layers {
		done, ok := layer.Body["done_when"].(map[string]any)
		if !ok {
			continue
		}
		leaves, ok := asTableArray(done["all"])
		if !ok {
			continue
		}
		for i, leaf := range leaves {
			if _, isJudge := leaf["judge"]; !isJudge {
				continue
			}
			id, ok := leaf["id"].(string)
			if !ok || id == "" {
				continue
			}
			if prev, dup := owner[id]; dup {
				at := childPos(childPos(childPos(Position{File: layer.File, Path: layer.ID}, "done_when"), "all"), fmt.Sprintf("[%d]", i))
				return nil, newDiag(CodeExtendsJudgeIDDuplicate, LayerSemantic, at,
					fmt.Sprintf("judge id %q is already declared by %q earlier in this extends chain", id, prev))
			}
			owner[id] = layer.ID
		}
	}
	judges := make(map[string]bool, len(owner))
	for id := range owner {
		judges[id] = true
	}
	return judges, nil
}

// schemaKeyRules enforces the extends composition rules for inputs_schema
// and state_schema, walking layers root first: a new key is free to add: an
// existing key may only gain a default where none is set anywhere in the
// chain so far, and any other change to an existing key's definition is a
// load error. It returns the union of declared property keys, which is the
// composed contract a completion predicate resolves `self.state.*` against.
func schemaKeyRules(field string, layers []*Definition) (map[string]bool, error) {
	known := map[string]map[string]any{}
	hasDefault := map[string]bool{}
	for _, layer := range layers {
		schema, ok := layer.Body[field].(map[string]any)
		if !ok {
			continue
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		pos := childPos(childPos(Position{File: layer.File, Path: layer.ID}, field), "properties")
		for _, key := range sortedKeys(props) {
			prop, ok := props[key].(map[string]any)
			if !ok {
				continue // a shape ValidateDefinition already rejected
			}
			at := childPos(pos, key)
			_, declaresDefault := prop["default"]
			candidate := withoutDefault(prop)
			existing, seen := known[key]
			if !seen {
				known[key] = candidate
				hasDefault[key] = declaresDefault
				continue
			}
			if !reflect.DeepEqual(candidate, existing) {
				return nil, newDiag(CodeExtendsSchemaType, LayerSemantic, at,
					fmt.Sprintf("%s.%s redefines a constraint the inner chain already fixed; only a new default may be added", field, key))
			}
			if declaresDefault {
				if hasDefault[key] {
					return nil, newDiag(CodeExtendsDefaultRedeclared, LayerSemantic, at,
						fmt.Sprintf("%s.%s already has a default in the inner chain; a later layer may not redeclare it", field, key))
				}
				hasDefault[key] = true
			}
		}
	}
	keys := make(map[string]bool, len(known))
	for key := range known {
		keys[key] = true
	}
	return keys, nil
}

func withoutDefault(prop map[string]any) map[string]any {
	out := make(map[string]any, len(prop))
	for k, v := range prop {
		if k == "default" {
			continue
		}
		out[k] = v
	}
	return out
}
