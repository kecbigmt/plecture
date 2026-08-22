package configlang

import (
	"fmt"
	"strings"
)

// ValidateTaskContracts is the half of a task document's validation that
// needs the rest of the layer: it resolves the observer the document
// declares, then checks every name the document reads against the contract
// that declares it — a state key against the observer's or this document's
// state_schema, and a chain's judge id against this document's done_when.
//
// It is separate from ValidateDefinition so that root availability, which
// needs nothing but the document, is always decided first: a value reaching
// a root its surface does not offer is that error, never a missing key in a
// contract the root never had.
func (v Validation) ValidateTaskContracts(def *Definition, r *Registry) error {
	if def.Kind != KindTask {
		return nil
	}
	pos := Position{File: def.File, Path: def.ID}
	at := childPos(pos, "resource_observer")
	raw, ok := def.Body["resource_observer"]
	if !ok {
		return newDiag(CodeFieldRequired, LayerStructural, at,
			"a task document declares the resource observer it is written for")
	}
	ref, err := staticRef(raw, at.Path)
	if err != nil {
		return err
	}
	observer, err := r.ExpectKind(ref, v.From, KindResourceObserver, at.Path)
	if err != nil {
		return err
	}

	c := taskContracts{
		resource: declaredState(observer.Body),
		self:     declaredState(def.Body),
		judges:   declaredJudges(def.Body),
	}
	if err := c.checkPredicate(def.Body, "done_when", pos); err != nil {
		return err
	}
	chains, err := tableArray(def.Body, "chains", pos)
	if err != nil {
		return err
	}
	for i, chain := range chains {
		chainAt := childPos(childPos(pos, "chains"), fmt.Sprintf("[%d]", i))
		if err := c.checkPredicate(chain, "when", chainAt); err != nil {
			return err
		}
		if err := c.checkChainInputs(chain, chainAt); err != nil {
			return err
		}
	}
	for _, match := range bodyProjection.FindAllStringSubmatch(def.Instruction, -1) {
		if err := c.resolve(match[1], childPos(pos, "instruction")); err != nil {
			return err
		}
	}
	return nil
}

// contract is one JSON Schema document's property set, or the fact that this
// pass cannot see it: a schema kept in a separate file resolves nothing
// here, and a document is not rejected against a contract nobody read.
type contract struct {
	readable bool
	props    map[string]bool
}

// taskContracts are the three things a task document's names resolve
// against: what its observer publishes, what it keeps itself, and which
// judges its completion predicate declares.
type taskContracts struct {
	resource contract
	self     contract
	judges   map[string]bool
}

// declaredState reads a definition's inline state_schema. An absent schema
// is an empty contract rather than an unreadable one — a document that keeps
// no state of its own publishes no key to read.
func declaredState(body map[string]any) contract {
	if _, filed := body["state_schema_file"]; filed {
		return contract{}
	}
	c := contract{readable: true, props: map[string]bool{}}
	schema, ok := body["state_schema"].(map[string]any)
	if !ok {
		return c
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return c
	}
	for key := range props {
		c.props[key] = true
	}
	return c
}

func declaredJudges(body map[string]any) map[string]bool {
	ids := map[string]bool{}
	done, ok := body["done_when"].(map[string]any)
	if !ok {
		return ids
	}
	leaves, ok := asTableArray(done["all"])
	if !ok {
		return ids
	}
	for _, leaf := range leaves {
		if _, isJudge := leaf["judge"]; !isJudge {
			continue
		}
		if id, ok := leaf["id"].(string); ok {
			ids[id] = true
		}
	}
	return ids
}

// checkPredicate resolves the names one done_when or chain when reads: a
// check leaf's key path, the paths a computed leaf reads, and the judge id a
// chain fact waits on.
func (c taskContracts) checkPredicate(body map[string]any, field string, pos Position) error {
	raw, ok := body[field]
	if !ok {
		return nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil // a shape ValidateDefinition already rejected
	}
	leaves, ok := asTableArray(tbl["all"])
	if !ok {
		return nil
	}
	for i, leaf := range leaves {
		at := childPos(childPos(childPos(pos, field), "all"), fmt.Sprintf("[%d]", i))
		if path, ok := leaf["check"].(string); ok {
			if err := c.resolve(path, at); err != nil {
				return err
			}
		}
		if src, ok := leaf["expr"].(string); ok {
			for _, path := range expressionPaths(src, surfaceTaskCompletion) {
				if err := c.resolve(path, at); err != nil {
					return err
				}
			}
		}
		for _, fact := range []string{"judge_pending", "judge_action"} {
			id, ok := leaf[fact].(string)
			if !ok {
				continue
			}
			if !c.judges[id] {
				return newDiag(CodeFromPath, LayerSemantic, at,
					fmt.Sprintf("%q names no judge leaf this document's done_when declares", id))
			}
		}
	}
	return nil
}

func (c taskContracts) checkChainInputs(chain map[string]any, pos Position) error {
	inputs, ok := chain["inputs"].(map[string]any)
	if !ok {
		return nil
	}
	at := childPos(pos, "inputs")
	for _, key := range sortedKeys(inputs) {
		value, err := ParseValue(inputs[key], ClassData, childPos(at, key))
		if err != nil {
			return nil // a shape ValidateDefinition already rejected
		}
		switch value.Form {
		case FormFrom:
			if err := c.resolve(value.From, childPos(at, key)); err != nil {
				return err
			}
		case FormExpr:
			for _, path := range expressionPaths(value.Expr, surfaceChainInputs) {
				if err := c.resolve(path, childPos(at, key)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resolve checks one dotted path against the contract its root names.
// Anything else — resource.id, a work fact a chain passes on — names no
// contract this pass reads.
func (c taskContracts) resolve(path string, pos Position) error {
	segments := strings.Split(path, ".")
	if len(segments) < 3 || segments[1] != "state" {
		return nil
	}
	var target contract
	switch segments[0] {
	case "resource":
		target = c.resource
	case "self":
		target = c.self
	default:
		return nil
	}
	if !target.readable || target.props[segments[2]] {
		return nil
	}
	return newDiag(CodeFromPath, LayerSemantic, pos,
		fmt.Sprintf("%q names no property the %s.state contract declares", path, segments[0]))
}

// expressionPaths returns the dotted paths one expression reads. An
// expression that does not parse yields none: checkExpression has already
// reported that, and this pass has nothing to add.
func expressionPaths(src string, s *Surface) []string {
	env, err := s.env()
	if err != nil {
		return nil
	}
	parsed, issues := env.Parse(src)
	if issues != nil && issues.Err() != nil {
		return nil
	}
	var paths []string
	collect := func(path string, bound map[string]bool) error {
		head, _, _ := strings.Cut(path, ".")
		if !bound[head] {
			paths = append(paths, path)
		}
		return nil
	}
	if err := walkExpr(parsed.NativeRep().Expr(), map[string]bool{}, env, collect, Position{}); err != nil {
		return nil
	}
	return paths
}
