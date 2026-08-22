package configlang

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
		if err := v.checkChainWorkflowInputs(chain, chainAt, r); err != nil {
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

// checkChainWorkflowInputs checks a chain against the contract of the workflow
// it spawns. Only the required half of that contract is checked here: whether
// a key the target does not declare may still be handed over is the
// dispatch-time passthrough question, and this corpus's own valid chain
// fixture hands over three such keys, so enforcing the closed half would
// reject the specification's worked example.
//
// A key the target requires and the chain omits is PLECTURE-CFG-FIELD-REQUIRED
// because that is the rule broken — a required field is absent — even though
// resolving the target is a semantic step; the diagnostics table documents no
// other code for it.
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
	inputs, _ := chain["inputs"].(map[string]any)
	for _, key := range requiredProperties(workflow.Body["inputs_schema"]) {
		if _, ok := inputs[key]; !ok {
			return newDiag(CodeFieldRequired, LayerStructural, childPos(pos, "inputs"),
				fmt.Sprintf("workflow %q requires the input %q, which this chain does not hand it", ref, key))
		}
	}
	return nil
}

func requiredProperties(raw any) []string {
	schema, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
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
