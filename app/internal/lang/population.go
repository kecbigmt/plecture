package lang

import (
	"fmt"
	"strings"
	"time"
)

func (v Validation) validateObserverQuery(def *Definition, pos Position) error {
	raw, ok := def.Body["query"]
	if !ok {
		return nil
	}
	at := childPos(pos, "query")
	query, err := table(raw, at)
	if err != nil {
		return err
	}
	if err := rejectUnknownFields(query, at, "inputs_schema", "item_schema", "poll", "subscribe"); err != nil {
		return err
	}
	for _, field := range []string{"inputs_schema", "item_schema"} {
		schema, declared := query[field]
		if !declared {
			return newDiag(CodeFieldRequired, LayerStructural, childPos(at, field),
				fmt.Sprintf("a query declares its shared `%s`", field))
		}
		if _, err := table(schema, childPos(at, field)); err != nil {
			return err
		}
		if err := checkNoTaggedValues(schema, childPos(at, field)); err != nil {
			return err
		}
	}
	if _, poll := query["poll"]; !poll {
		if _, subscribe := query["subscribe"]; !subscribe {
			return newDiag(CodeFieldRequired, LayerStructural, at,
				"a query declares at least one of `poll` or `subscribe`")
		}
	}
	for _, means := range []string{"poll", "subscribe"} {
		if err := v.action(query, means, surfaceObserverQuery, at); err != nil {
			return err
		}
		if action, ok := query[means]; ok {
			if err := validateQueryInputPaths(action, query["inputs_schema"].(map[string]any), childPos(at, means)); err != nil {
				return err
			}
		}
	}
	return validateQueryItemSchema(def, query["item_schema"].(map[string]any), at)
}

func validateQueryInputPaths(value any, schema map[string]any, pos Position) error {
	switch node := value.(type) {
	case map[string]any:
		if from, ok := node["from"].(string); ok {
			if err := validateQueryInputPath(from, schema, pos); err != nil {
				return err
			}
		}
		if expr, ok := node["expr"].(string); ok {
			for _, path := range expressionPaths(expr, surfaceObserverQuery) {
				if err := validateQueryInputPath(path, schema, pos); err != nil {
					return err
				}
			}
		}
		for key, child := range node {
			if err := validateQueryInputPaths(child, schema, childPos(pos, key)); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range node {
			if err := validateQueryInputPaths(child, schema, childPos(pos, fmt.Sprintf("[%d]", i))); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateQueryInputPath(from string, schema map[string]any, pos Position) error {
	path, isInput := strings.CutPrefix(from, "inputs.")
	if isInput && !schemaDeclaresPath(schema, strings.Split(path, ".")) {
		return newDiag(CodeFromPath, LayerSemantic, pos,
			fmt.Sprintf("query projection %q is not declared by inputs_schema", from))
	}
	return nil
}

func schemaDeclaresPath(schema map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	property, ok := properties[path[0]].(map[string]any)
	if !ok {
		return false
	}
	if len(path) == 1 {
		return true
	}
	return schemaDeclaresPath(property, path[1:])
}

func validateQueryItemSchema(def *Definition, schema map[string]any, pos Position) error {
	if typ, ok := schema["type"].(string); !ok || typ != "object" {
		return newDiag(CodeFieldType, LayerStructural, childPos(pos, "item_schema.type"),
			"a query item schema has type `object`")
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "resource" {
		return newDiag(CodeFieldRequired, LayerStructural, childPos(pos, "item_schema.required"),
			"a query item requires only its `resource` property")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return newDiag(CodeFieldRequired, LayerStructural, childPos(pos, "item_schema.properties.resource"),
			"a query item declares its string `resource` property")
	}
	resource, ok := properties["resource"].(map[string]any)
	if !ok || resource["type"] != "string" {
		return newDiag(CodeFieldType, LayerStructural, childPos(pos, "item_schema.properties.resource"),
			"a query item's `resource` property is a string")
	}
	state := declaredState(def.Body)
	for key := range properties {
		if key != "resource" && state[key] {
			return newDiag(CodeFromPath, LayerSemantic, childPos(pos, "item_schema.properties."+key),
				fmt.Sprintf("query item property %q duplicates a state_schema fact; observe is the state authority", key))
		}
	}
	return nil
}

func (v Validation) validateWorkflowPopulations(def *Definition, pos Position) error {
	entries, err := tableArray(def.Body, "populations", pos)
	if err != nil || len(entries) == 0 {
		return err
	}
	if v.From.IsPlugin {
		return newDiag(CodePopulationContract, LayerSemantic, childPos(pos, "populations"),
			"workflow populations are user-owned deployment policy and may not be declared by a plugin")
	}
	names := make(map[string]bool, len(entries))
	for i, entry := range entries {
		at := childPos(childPos(pos, "populations"), fmt.Sprintf("[%d]", i))
		if err := rejectUnknownFields(entry, at, "name", "resource_observer", "query", "session", "poll_every", "expire_after", "auto_down", "auto_destroy"); err != nil {
			return err
		}
		for _, field := range []string{"name", "resource_observer", "query"} {
			if _, ok := entry[field]; !ok {
				return newDiag(CodeFieldRequired, LayerStructural, childPos(at, field),
					fmt.Sprintf("a population declares `%s`", field))
			}
		}
		name, ok := entry["name"].(string)
		if !ok {
			return newDiag(CodeFieldType, LayerStructural, childPos(at, "name"), "a population name is a string")
		}
		if !isValidID(name) {
			return newDiag(CodeIDInvalid, LayerStructural, childPos(at, "name"),
				fmt.Sprintf("population name %q does not match ^[A-Za-z_][A-Za-z0-9_]*$", name))
		}
		if names[name] {
			return newDiag(CodeIDDuplicate, LayerSemantic, childPos(at, "name"),
				fmt.Sprintf("population name %q is declared more than once in workflow %q", name, def.ID))
		}
		names[name] = true
		if _, err := staticRef(entry["resource_observer"], childPos(at, "resource_observer").Path); err != nil {
			return err
		}
		query, err := table(entry["query"], childPos(at, "query"))
		if err != nil {
			return err
		}
		if err := checkNoTaggedValues(query, childPos(at, "query")); err != nil {
			return err
		}
		if err := v.validatePopulationSession(entry, at); err != nil {
			return err
		}
		for _, field := range []string{"auto_down", "auto_destroy"} {
			if raw, ok := entry[field]; ok {
				if _, ok := raw.(bool); !ok {
					return newDiag(CodeFieldType, LayerStructural, childPos(at, field), field+" is a boolean")
				}
			}
		}
	}
	return nil
}

// ValidatePopulationContracts is separate from ValidatePlan because config
// loading must reject an invalid population even when the caller does not
// compile an executable plan.
func (v Validation) ValidatePopulationContracts(workflow *Definition, registry *Registry) error {
	entries, err := tableArray(workflow.Body, "populations", Position{File: workflow.File, Path: workflow.ID})
	if err != nil || len(entries) == 0 {
		return err
	}
	for i, entry := range entries {
		at := Position{File: workflow.File, Path: fmt.Sprintf("%s.populations.[%d]", workflow.ID, i)}
		observerRef := entry["resource_observer"].(string)
		observer, err := registry.ExpectKind(observerRef, v.From, KindResourceObserver, at.Path+".resource_observer")
		if err != nil {
			return err
		}
		query, ok := observer.Body["query"].(map[string]any)
		if !ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "resource_observer"),
				fmt.Sprintf("resource observer %q declares no query face", observerRef))
		}
		inputSchema, err := CompileInlineSchema(query["inputs_schema"].(map[string]any), "plect:population-query-inputs")
		if err != nil {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "query"), err.Error())
		}
		if err := inputSchema.Validate(entry["query"]); err != nil {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "query"),
				"population query parameters do not satisfy the observer query inputs_schema: "+err.Error())
		}
		if err := validatePopulationTiming(entry, query, at); err != nil {
			return err
		}
		if err := v.validatePopulationSessionContracts(entry, observer, query, registry, at); err != nil {
			return err
		}
	}
	return nil
}

func validatePopulationTiming(entry, query map[string]any, at Position) error {
	_, poll := query["poll"]
	_, subscribe := query["subscribe"]
	if poll {
		if _, ok := entry["poll_every"]; !ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "poll_every"),
				"poll_every is required when the observer query declares poll")
		}
		if _, ok := entry["expire_after"]; ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "expire_after"),
				"expire_after is forbidden when the observer query declares poll")
		}
	} else if subscribe {
		if _, ok := entry["expire_after"]; !ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "expire_after"),
				"expire_after is required for a subscribe-only observer query")
		}
		if _, ok := entry["poll_every"]; ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "poll_every"),
				"poll_every is forbidden for a subscribe-only observer query")
		}
	}
	for _, field := range []string{"poll_every", "expire_after"} {
		raw, ok := entry[field]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return newDiag(CodeFieldType, LayerStructural, childPos(at, field), field+" is a duration string")
		}
		duration, parseErr := parsePopulationDuration(value)
		if parseErr != nil || duration <= 0 {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, field), field+" is a positive duration")
		}
	}
	return nil
}

func parsePopulationDuration(value string) (time.Duration, error) {
	if len(value) > 1 && strings.HasSuffix(value, "d") {
		hours, err := time.ParseDuration(strings.TrimSuffix(value, "d") + "h")
		if err != nil {
			return 0, err
		}
		return hours * 24, nil
	}
	return time.ParseDuration(value)
}

func (v Validation) validatePopulationSessionContracts(entry map[string]any, observer *Definition, query map[string]any, registry *Registry, at Position) error {
	session, _ := entry["session"].(map[string]any)
	if taskRef, ok := session["task"].(string); ok {
		task, err := registry.ExpectKind(taskRef, v.From, KindTask, at.Path+".session.task")
		if err != nil {
			return err
		}
		taskValidation := Validation{From: registry.OwnerOf(task)}
		chain, err := taskValidation.ExtendsChain(task, registry)
		if err != nil {
			return err
		}
		root := task
		if len(chain) > 0 {
			root = chain[0]
		}
		taskObserverRef, ok := root.Body["resource_observer"].(string)
		if !ok {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "session.task"),
				fmt.Sprintf("task %q declares no resource_observer", taskRef))
		}
		taskObserver, err := registry.ExpectKind(taskObserverRef, registry.OwnerOf(root), KindResourceObserver, root.ID+".resource_observer")
		if err != nil {
			return err
		}
		if taskObserver != observer {
			return newDiag(CodePopulationContract, LayerSemantic, childPos(at, "session.task"),
				"the initial task and population must resolve to the same resource observer")
		}
	}
	itemSchema := query["item_schema"].(map[string]any)
	properties, _ := itemSchema["properties"].(map[string]any)
	inputs, _ := session["inputs"].(map[string]any)
	for key, raw := range inputs {
		value, err := ParseValue(raw, ClassData, childPos(at, "session.inputs."+key))
		if err != nil || value.Form != FormFrom {
			continue
		}
		if value.From == "resource.id" {
			continue
		}
		property, ok := strings.CutPrefix(value.From, "item.")
		if !ok || strings.Contains(property, ".") || properties[property] == nil {
			return newDiag(CodeFromPath, LayerSemantic, childPos(at, "session.inputs."+key),
				fmt.Sprintf("%q is not declared by the query item_schema", value.From))
		}
	}
	return nil
}

func (v Validation) validatePopulationSession(entry map[string]any, pos Position) error {
	raw, ok := entry["session"]
	if !ok {
		return nil
	}
	at := childPos(pos, "session")
	session, err := table(raw, at)
	if err != nil {
		return err
	}
	if err := rejectUnknownFields(session, at, "task", "inputs", "destroy"); err != nil {
		return err
	}
	if raw, ok := session["task"]; ok {
		if _, err := staticRef(raw, childPos(at, "task").Path); err != nil {
			return err
		}
	}
	if err := v.valueTable(session, "inputs", ClassData, surfaceWorkflowPopulationInputs, at); err != nil {
		return err
	}
	if raw, ok := session["destroy"]; ok {
		destroyAt := childPos(at, "destroy")
		destroy, err := table(raw, destroyAt)
		if err != nil {
			return err
		}
		if err := rejectUnknownFields(destroy, destroyAt, "force"); err != nil {
			return err
		}
		if raw, ok := destroy["force"]; ok {
			if _, ok := raw.(bool); !ok {
				return newDiag(CodeFieldType, LayerStructural, childPos(destroyAt, "force"), "force is a boolean")
			}
		}
	}
	return nil
}
