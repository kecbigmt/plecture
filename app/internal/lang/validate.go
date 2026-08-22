package lang

import (
	"fmt"
	"regexp"
)

// Validation carries what one definition's value surfaces are checked
// against: the layer that wrote the config, and the executables a bin
// reference resolves against.
type Validation struct {
	From        Ownership
	Executables *ExecutableRegistry
}

// ValidateDefinition walks exactly the surfaces docs/language/values.md
// declares for a definition's kind. Per-kind field applicability, the
// nesting joint's agreement rules, and contract-key resolution are other
// checks, and a definition that breaks one of those still passes here.
func (v Validation) ValidateDefinition(def *Definition) error {
	pos := Position{File: def.File, Path: def.ID}
	if err := checkKindSurface(def, pos); err != nil {
		return err
	}
	if err := v.checkStaticTopology(def, pos); err != nil {
		return err
	}
	for _, field := range []string{"inputs_schema", "outputs_schema", "locals_schema", "state_schema", "input_schema"} {
		if contract, ok := def.Body[field]; ok {
			if err := checkNoTaggedValues(contract, childPos(pos, field)); err != nil {
				return err
			}
		}
	}
	switch def.Kind {
	case KindEffect:
		return v.validateEffect(def, pos)
	case KindChannel:
		return v.validateChannel(def, pos)
	case KindWorkspaceProvider:
		return v.validateProvider(def, pos)
	case KindResourceObserver:
		return v.validateObserver(def, pos)
	case KindWorkflow:
		return v.validateWorkflow(def, pos)
	case KindTask:
		return v.validateTask(def, pos)
	}
	return nil
}

func (v Validation) validateEffect(def *Definition, pos Position) error {
	if err := v.action(def.Body, "setup", surfaceEffectSetup, pos); err != nil {
		return err
	}
	if err := v.action(def.Body, "cleanup", surfaceEffectCleanup, pos); err != nil {
		return err
	}
	if health, ok := def.Body["health"]; ok {
		tbl, err := table(health, childPos(pos, "health"))
		if err != nil {
			return err
		}
		for _, probe := range []string{"alive", "activity"} {
			if err := v.action(tbl, probe, surfaceEffectHealth, childPos(pos, "health")); err != nil {
				return err
			}
		}
	}
	if terminal, ok := def.Body["terminal"]; ok {
		tbl, err := table(terminal, childPos(pos, "terminal"))
		if err != nil {
			return err
		}
		for _, verb := range terminalVerbOrder {
			if err := v.action(tbl, verb, surfaceEffectTerminal, childPos(pos, "terminal")); err != nil {
				return err
			}
		}
	}
	if inner, ok := def.Body["inner"]; ok {
		tbl, err := table(inner, childPos(pos, "inner"))
		if err != nil {
			return err
		}
		for _, field := range []string{"inputs", "env"} {
			if err := v.valueTable(tbl, field, ClassData, surfaceEffectInner, childPos(pos, "inner")); err != nil {
				return err
			}
		}
	}
	if outputs, ok := def.Body["outputs"]; ok {
		tbl, err := table(outputs, childPos(pos, "outputs"))
		if err != nil {
			return err
		}
		if err := v.valueTable(tbl, "bind", ClassData, surfaceEffectOutputsBind, childPos(pos, "outputs")); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) validateChannel(def *Definition, pos Position) error {
	kind, _ := def.Body["type"].(string)
	delivery := map[string]any{"type": kind}
	switch kind {
	case "unix_socket":
		for _, field := range []string{"path", "body"} {
			raw, ok := def.Body[field]
			if !ok {
				return newDiag(CodeFieldRequired, LayerStructural, childPos(pos, field),
					"a unix_socket channel declares path and body")
			}
			if err := v.value(raw, ClassData, surfaceChannelDelivery, childPos(pos, field)); err != nil {
				return err
			}
		}
		for _, field := range append(execOnlyFields, shellOnlyFields...) {
			if _, ok := def.Body[field]; ok {
				return newDiag(CodeActionVariant, LayerStructural, childPos(pos, field),
					fmt.Sprintf("%s belongs to a process delivery, not to unix_socket", field))
			}
		}
	case actionExec, actionShell:
		for _, field := range append(execOnlyFields, shellOnlyFields...) {
			if raw, ok := def.Body[field]; ok {
				delivery[field] = raw
			}
		}
		for _, field := range []string{"path", "body"} {
			if _, ok := def.Body[field]; ok {
				return newDiag(CodeActionVariant, LayerStructural, childPos(pos, field),
					fmt.Sprintf("%s belongs to unix_socket delivery, not to %s", field, kind))
			}
		}
		action, err := ParseAction(delivery, pos)
		if err != nil {
			return err
		}
		if err := v.checkAction(action, surfaceChannelDelivery, pos); err != nil {
			return err
		}
	default:
		return newDiag(CodeActionTypeUnknown, LayerStructural, childPos(pos, "type"),
			fmt.Sprintf("a channel's type is unix_socket, exec, or shell, not %v", def.Body["type"]))
	}
	if timeout, ok := def.Body["timeout"]; ok {
		value, err := ParseValue(timeout, ClassData, childPos(pos, "timeout"))
		if err != nil {
			return err
		}
		if value.Form != FormLiteral && value.Form != FormFrom {
			return newDiag(CodeChannelTimeoutRoot, LayerStructural, childPos(pos, "timeout"),
				"a delivery deadline is an author-declared parameter, so timeout is a literal or a projection of inputs")
		}
		if err := v.checkValue(value, surfaceChannelTimeout, childPos(pos, "timeout")); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) validateProvider(def *Definition, pos Position) error {
	if name, ok := def.Body["name"]; ok {
		if err := v.value(name, ClassData, surfaceProviderName, childPos(pos, "name")); err != nil {
			return err
		}
	}
	for _, hook := range []struct {
		field   string
		surface *Surface
	}{
		{"setup", surfaceProviderSetup},
		{"cleanup", surfaceProviderCleanup},
		{"subscribe", surfaceProviderSubscribe},
	} {
		if err := v.action(def.Body, hook.field, hook.surface, pos); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) validateObserver(def *Definition, pos Position) error {
	if err := v.action(def.Body, "observe", surfaceObserverObserve, pos); err != nil {
		return err
	}
	return v.action(def.Body, "finalize", surfaceObserverFinalize, pos)
}

func (v Validation) validateWorkflow(def *Definition, pos Position) error {
	if err := v.valueTable(def.Body, "display", ClassData, surfaceWorkflowDisplay, pos); err != nil {
		return err
	}
	if inputs, ok := def.Body["workspace_provider_inputs"]; ok {
		at := childPos(pos, "workspace_provider_inputs")
		tbl, err := table(inputs, at)
		if err != nil {
			return err
		}
		for _, key := range sortedKeys(tbl) {
			// A workspace provider's hooks run before any node output
			// exists, so its parameters are literal data and no surface
			// environment applies.
			if _, err := ParseValue(tbl[key], ClassLiteral, childPos(at, key)); err != nil {
				return err
			}
		}
	}
	nodes, err := tableArray(def.Body, "nodes", pos)
	if err != nil {
		return err
	}
	for i, node := range nodes {
		at := childPos(childPos(pos, "nodes"), fmt.Sprintf("[%d]", i))
		if err := v.valueTable(node, "inputs", ClassData, surfaceWorkflowNodeInputs, at); err != nil {
			return err
		}
	}
	for i, channel := range v.eventChannels(def) {
		at := childPos(childPos(pos, "event.channel"), fmt.Sprintf("[%d]", i))
		if err := v.valueTable(channel, "inputs", ClassData, surfaceWorkflowNodeInputs, at); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) validateTask(def *Definition, pos Position) error {
	if err := v.completionPredicate(def.Body, "done_when", pos); err != nil {
		return err
	}
	chains, err := tableArray(def.Body, "chains", pos)
	if err != nil {
		return err
	}
	for i, chain := range chains {
		at := childPos(childPos(pos, "chains"), fmt.Sprintf("[%d]", i))
		if err := v.completionPredicate(chain, "when", at); err != nil {
			return err
		}
		if err := v.valueTable(chain, "inputs", ClassData, surfaceChainInputs, at); err != nil {
			return err
		}
	}
	return v.instructionBody(def, pos)
}

// completionPredicate checks the leaves of a done_when or a chain's when. A
// judge leaf is skipped rather than rejected: it carries prose for a
// reviewer, not a value over this surface's roots.
func (v Validation) completionPredicate(body map[string]any, field string, pos Position) error {
	raw, ok := body[field]
	if !ok {
		return nil
	}
	tbl, err := table(raw, childPos(pos, field))
	if err != nil {
		return err
	}
	leaves, err := tableArray(tbl, "all", childPos(pos, field))
	if err != nil {
		return err
	}
	for i, leaf := range leaves {
		at := childPos(childPos(childPos(pos, field), "all"), fmt.Sprintf("[%d]", i))
		if key, ok := leaf["check"]; ok {
			path, ok := key.(string)
			if !ok {
				return newDiag(CodeFieldType, LayerStructural, at, "check names a completion key path")
			}
			if !surfaceTaskCompletion.offers(path) {
				return newDiag(CodeFromRoot, surfaceTaskCompletion.rootLayer, at,
					fmt.Sprintf("%q is not a root the %s surface offers", path, surfaceTaskCompletion.Name))
			}
		}
		if expr, ok := leaf["expr"]; ok {
			src, ok := expr.(string)
			if !ok {
				return newDiag(CodeFieldType, LayerStructural, at, "expr holds one CEL expression")
			}
			if err := checkExpression(src, surfaceTaskCompletion, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// bodyProjection matches a projection in prose position. Control flow in an
// instruction body is an open decision, so a `{{ ... }}` holding anything
// but a dotted path is left to the transitional template forms the
// instruction assets carried in.
var bodyProjection = regexp.MustCompile(`\{\{\s*([a-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\s*\}\}`)

func (v Validation) instructionBody(def *Definition, pos Position) error {
	for _, match := range bodyProjection.FindAllStringSubmatch(def.Instruction, -1) {
		path := match[1]
		if !surfaceTaskInstruction.offers(path) {
			return newDiag(CodeFromRoot, surfaceTaskInstruction.rootLayer, childPos(pos, "instruction"),
				fmt.Sprintf("%q is not a root the %s surface offers", path, surfaceTaskInstruction.Name))
		}
	}
	return nil
}

// checkStaticTopology rejects a computed value on a field that determines
// topology, so the shape of a configuration is discoverable before anything
// is evaluated.
func (v Validation) checkStaticTopology(def *Definition, pos Position) error {
	if wp, ok := def.Body["workspace_provider"]; ok {
		if _, err := staticRef(wp, childPos(pos, "workspace_provider").Path); err != nil {
			return err
		}
	}
	if inner, ok := def.Body["inner"].(map[string]any); ok {
		if uses, ok := inner["uses"]; ok {
			if _, err := staticRef(uses, childPos(childPos(pos, "inner"), "uses").Path); err != nil {
				return err
			}
		}
	}
	nodes, err := tableArray(def.Body, "nodes", pos)
	if err != nil {
		return err
	}
	for i, node := range nodes {
		if uses, ok := node["uses"]; ok {
			at := childPos(childPos(childPos(pos, "nodes"), fmt.Sprintf("[%d]", i)), "uses")
			if _, err := staticRef(uses, at.Path); err != nil {
				return err
			}
		}
	}
	for i, channel := range v.eventChannels(def) {
		if uses, ok := channel["uses"]; ok {
			at := childPos(childPos(childPos(pos, "event.channel"), fmt.Sprintf("[%d]", i)), "uses")
			if _, err := staticRef(uses, at.Path); err != nil {
				return err
			}
		}
	}
	chains, err := tableArray(def.Body, "chains", pos)
	if err != nil {
		return err
	}
	for i, chain := range chains {
		if workflow, ok := chain["workflow"]; ok {
			at := childPos(childPos(childPos(pos, "chains"), fmt.Sprintf("[%d]", i)), "workflow")
			if _, err := staticRef(workflow, at.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v Validation) eventChannels(def *Definition) []map[string]any {
	event, ok := def.Body["event"].(map[string]any)
	if !ok {
		return nil
	}
	channels, _ := asTableArray(event["channel"])
	return channels
}

func (v Validation) action(body map[string]any, field string, s *Surface, pos Position) error {
	raw, ok := body[field]
	if !ok {
		return nil
	}
	at := childPos(pos, field)
	action, err := ParseAction(raw, at)
	if err != nil {
		return err
	}
	return v.checkAction(action, s, at)
}

func (v Validation) checkAction(a *Action, s *Surface, pos Position) error {
	if a.Bin != "" {
		if _, err := v.Executables.ResolveBin(a.Bin, v.From); err != nil {
			return err
		}
	}
	for _, value := range a.values() {
		if err := v.checkValue(value, s, value.Pos); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) valueTable(body map[string]any, field string, class ValueClass, s *Surface, pos Position) error {
	raw, ok := body[field]
	if !ok {
		return nil
	}
	at := childPos(pos, field)
	tbl, err := table(raw, at)
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(tbl) {
		if err := v.value(tbl[key], class, s, childPos(at, key)); err != nil {
			return err
		}
	}
	return nil
}

func (v Validation) value(raw any, class ValueClass, s *Surface, pos Position) error {
	value, err := ParseValue(raw, class, pos)
	if err != nil {
		return err
	}
	return v.checkValue(value, s, pos)
}

// checkValue applies the surface's evaluation environment to one value. A
// literal reaches no root and names no executable, so it needs no check
// here.
func (v Validation) checkValue(value *Value, s *Surface, pos Position) error {
	switch value.Form {
	case FormFrom:
		if !s.offers(value.From) {
			return newDiag(s.rootCode, s.rootLayer, pos,
				fmt.Sprintf("%q is not a root the %s surface offers", value.From, s.Name))
		}
	case FormExpr:
		return checkExpression(value.Expr, s, pos)
	case FormBin:
		if _, err := v.Executables.ResolveBin(value.Bin, v.From); err != nil {
			return err
		}
	case FormJSON:
		return v.checkOperand(value.JSON, s, pos)
	}
	return nil
}

func (v Validation) checkOperand(op *JSONOperand, s *Surface, pos Position) error {
	switch {
	case op == nil:
		return nil
	case op.Leaf != nil:
		return v.checkValue(op.Leaf, s, pos)
	case op.Object != nil:
		for _, key := range sortedOperandKeys(op.Object) {
			if err := v.checkOperand(op.Object[key], s, childPos(pos, key)); err != nil {
				return err
			}
		}
	default:
		for i, child := range op.Array {
			if err := v.checkOperand(child, s, childPos(pos, fmt.Sprintf("[%d]", i))); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedOperandKeys(obj map[string]*JSONOperand) []string {
	keys := make(map[string]any, len(obj))
	for k := range obj {
		keys[k] = nil
	}
	return sortedKeys(keys)
}

func table(raw any, pos Position) (map[string]any, error) {
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, newDiag(CodeFieldType, LayerStructural, pos, "this field is a table")
	}
	return tbl, nil
}

func tableArray(body map[string]any, field string, pos Position) ([]map[string]any, error) {
	raw, ok := body[field]
	if !ok {
		return nil, nil
	}
	arr, ok := asTableArray(raw)
	if !ok {
		return nil, newDiag(CodeFieldType, LayerStructural, childPos(pos, field),
			"this field is an array of tables")
	}
	return arr, nil
}
