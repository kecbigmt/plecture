package lang

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateTaskContracts is separate from ValidateDefinition because it needs
// the rest of the layer, and because the resulting order is load-bearing:
// root availability, which needs nothing but the document, is always decided
// first, so a value reaching a root its surface does not offer is reported as
// that and never as a missing key in a contract the root never had.
func (v Validation) ValidateTaskContracts(def *Definition, r *Registry) error {
	if def.Kind != KindTask {
		return nil
	}
	pos := Position{File: def.File, Path: def.ID}

	chain, err := v.ExtendsChain(def, r)
	if err != nil {
		return err
	}
	// layers is the whole composed chain, root first and def itself last: the
	// order composition accumulates in, since each layer's own additions land
	// on top of everything before it.
	layers := append(append([]*Definition{}, chain...), def)

	root := layers[0]
	rootFrom := v.From
	if root != def {
		rootFrom = r.OwnerOf(root)
	}
	at := childPos(Position{File: root.File, Path: root.ID}, "resource_observer")
	raw, ok := root.Body["resource_observer"]
	if !ok {
		return newDiag(CodeFieldRequired, LayerStructural, at,
			"a task document declares the resource observer it is written for")
	}
	ref, err := staticRef(raw, at.Path)
	if err != nil {
		return err
	}
	observer, err := r.ExpectKind(ref, rootFrom, KindResourceObserver, at.Path)
	if err != nil {
		return err
	}

	if err := RejectSchemaFileInChain(layers); err != nil {
		return err
	}
	// inputs_schema carries no contract this pass resolves against, but its
	// extends composition rules are load-time rules regardless.
	if _, err := SchemaKeyRules("inputs_schema", layers); err != nil {
		return err
	}
	selfKeys, err := SchemaKeyRules("state_schema", layers)
	if err != nil {
		return err
	}
	judges, err := ComposedJudges(layers)
	if err != nil {
		return err
	}
	if _, err := ComposedChainIDs(layers); err != nil {
		return err
	}

	c := taskContracts{
		resource: declaredState(observer.Body),
		self:     selfKeys,
		judges:   judges,
	}
	if err := c.checkPredicate(def.Body, "done_when", pos); err != nil {
		return err
	}
	chains, err := tableArray(def.Body, "chains", pos)
	if err != nil {
		return err
	}
	for i, chainTbl := range chains {
		chainAt := childPos(childPos(pos, "chains"), fmt.Sprintf("[%d]", i))
		if err := c.checkPredicate(chainTbl, "when", chainAt); err != nil {
			return err
		}
		if err := c.checkChainResource(chainTbl, chainAt); err != nil {
			return err
		}
		if err := c.checkChainInputs(chainTbl, chainAt); err != nil {
			return err
		}
		if err := v.checkChainWorkflowInputs(chainTbl, chainAt, r); err != nil {
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

type taskContracts struct {
	resource map[string]bool
	self     map[string]bool
	judges   map[string]bool
}

// declaredState treats a contract the definition does not carry — absent, or
// deferred to a state_schema_file the language defines no resolution rule for
// — as declaring no key, so a document reading one is rejected rather than
// exempted: an unresolvable contract is the case a key check exists for.
func declaredState(body map[string]any) map[string]bool {
	props := map[string]bool{}
	schema, ok := body["state_schema"].(map[string]any)
	if !ok {
		return props
	}
	declared, ok := schema["properties"].(map[string]any)
	if !ok {
		return props
	}
	for key := range declared {
		props[key] = true
	}
	return props
}

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

// checkChainResource resolves the keys the spawned session's resource is
// projected from, against the same two contracts a chain's inputs resolve
// against.
func (c taskContracts) checkChainResource(chain map[string]any, pos Position) error {
	raw, declared := chain["resource"]
	if !declared {
		return nil
	}
	at := childPos(pos, "resource")
	value, err := ParseValue(raw, ClassData, at)
	if err != nil {
		return nil // a shape ValidateDefinition already rejected
	}
	switch value.Form {
	case FormFrom:
		return c.resolve(value.From, at)
	case FormExpr:
		for _, path := range expressionPaths(value.Expr, surfaceChainInputs) {
			if err := c.resolve(path, at); err != nil {
				return err
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

// checkChainWorkflowInputs reports what the target workflow's own contract
// rejects, in the two codes naming those rules: a required field absent, and
// a field outside the surface that declares it. Resolving the target is a
// semantic step, but the diagnostics table documents no other code for either
// rule, and the rule broken is what a code identifies.
//
// A key the target does not declare is only rejected where the target closes
// its contract, because that is the schema's own answer: `additionalProperties`
// left open is a workflow saying it accepts what it did not enumerate.
func (v Validation) checkChainWorkflowInputs(chain map[string]any, pos Position, r *Registry) error {
	raw, ok := chain["workflow"]
	if !ok {
		return nil
	}
	site := childPos(pos, "workflow")
	ref, err := staticRef(raw, site.Path)
	if err != nil {
		return err
	}
	workflow, err := r.ExpectKind(ref, v.From, KindWorkflow, site.Path)
	if err != nil {
		return err
	}
	schema, _ := workflow.Body["inputs_schema"].(map[string]any)
	inputs, _ := chain["inputs"].(map[string]any)
	at := childPos(pos, "inputs")
	for _, key := range requiredProperties(schema) {
		if _, ok := inputs[key]; !ok {
			return newDiag(CodeFieldRequired, LayerStructural, at,
				fmt.Sprintf("workflow %q requires the input %q, which this chain does not hand it", ref, key))
		}
	}
	if open, _ := schema["additionalProperties"].(bool); open || schema["additionalProperties"] == nil {
		return nil
	}
	declared := declaredProperties(schema)
	for _, key := range sortedKeys(inputs) {
		if !declared[key] {
			return newDiag(CodeFieldUnknown, LayerStructural, childPos(at, key),
				fmt.Sprintf("workflow %q declares no input %q, and closes its inputs contract", ref, key))
		}
	}
	return nil
}

func requiredProperties(schema map[string]any) []string {
	list, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	var keys []string
	for _, entry := range list {
		if key, ok := entry.(string); ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func declaredProperties(schema map[string]any) map[string]bool {
	declared := map[string]bool{}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return declared
	}
	for key := range props {
		declared[key] = true
	}
	return declared
}

// resolve returns early for anything but a state read, because the rest —
// resource.id, a work fact a chain passes on — names no contract this pass
// reads.
func (c taskContracts) resolve(path string, pos Position) error {
	segments := strings.Split(path, ".")
	if len(segments) < 3 || segments[1] != "state" {
		return nil
	}
	var declared map[string]bool
	switch segments[0] {
	case "resource":
		declared = c.resource
	case "self":
		declared = c.self
	default:
		return nil
	}
	if declared[segments[2]] {
		return nil
	}
	return newDiag(CodeFromPath, LayerSemantic, pos,
		fmt.Sprintf("%q names no property the %s.state contract declares", path, segments[0]))
}

// expressionPaths yields nothing for an expression that does not parse:
// checkExpression has already reported that, and this pass has nothing to
// add.
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
	collect := func(path string) error {
		paths = append(paths, path)
		return nil
	}
	if err := walkExpr(parsed.NativeRep().Expr(), map[string]bool{}, env, collect, Position{}); err != nil {
		return nil
	}
	return paths
}

// CompletionExpressionPaths lists the rooted paths one completion expression
// reads, using the same walk the contract check resolves them with — so a
// reader asking what a predicate depends on cannot drift from what the
// validator checked. An expression that does not parse yields nothing:
// checkExpression has already reported that.
func CompletionExpressionPaths(src string) []string {
	return expressionPaths(src, surfaceTaskCompletion)
}
