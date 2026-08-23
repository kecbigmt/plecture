package config

import (
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// loadWorkflowDocument reads every `kind = "workflow"` declaration in one
// definition document, checking each against the workflow surface before the
// cascade merges it with the layers around it.
func (c *Config) loadWorkflowDocument(path string, layer layerDir) ([]*lang.Definition, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := lang.ParseDefinitionDocument(path, src)
	if err != nil {
		return nil, err
	}
	validation := lang.Validation{
		From:        lang.Ownership{IsPlugin: layer.plugin},
		Executables: c.binResolver(path),
	}
	for _, def := range parsed {
		if def.Kind != lang.KindWorkflow {
			return nil, fmt.Errorf("%s: %q declares kind %q; a definition under workflows/ is a workflow", path, def.ID, def.Kind)
		}
		if layer.workspaceDir {
			if err := checkWorkspaceDirFragment(def, path); err != nil {
				return nil, err
			}
		}
		// Validation runs per document rather than once over the merged
		// definition so a diagnostic names the layer that wrote the offending
		// value; the merged definition's own topology is checked when it is
		// decoded.
		if err := validation.ValidateDefinition(def); err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

// checkWorkspaceDirFragment enforces the node-addition-only rule for a
// workflow document inside the workspace directory. That layer is clone
// content: an attacker-controlled repository must not be able to name the
// provider a session runs on, wire an event channel to a delivery primitive,
// or supply a clock that drives execution with nobody in the loop. `nodes` is
// stated as an allowlist rather than the offending fields as a denylist, so a
// field added to the workflow surface later is closed here by default.
func checkWorkspaceDirFragment(def *lang.Definition, path string) error {
	var offending []string
	for _, field := range sortedBodyKeys(def.Body) {
		if field != "nodes" {
			offending = append(offending, field)
		}
	}
	if len(offending) > 0 {
		return fmt.Errorf("workflow %s: a `.plect/workflows/` document inside the workspace directory may only add [[nodes]]; %v must move to a trusted layer (global config, plugin, or a directory above the workspace dir)", path, offending)
	}
	return nil
}

// workflowFrom reads the fields the runtime needs off one merged workflow
// declaration. sourcePath is the shallowest layer that declared the id, which
// is what anchors a relative schema path and which layer's namespace a
// reference written in it resolves against.
func workflowFrom(def *lang.Definition, sourcePath string) (WorkflowFile, error) {
	pos := lang.Position{File: def.File, Path: def.ID}
	w := WorkflowFile{
		ID:         def.ID,
		Definition: def,
		SourcePath: sourcePath,
		BaseDir:    configFileDir(sourcePath),
	}
	for _, field := range []struct {
		key   string
		field *string
	}{
		{"name", &w.Name},
		{"description", &w.Description},
		{"workspace_provider", &w.WorkspaceProvider},
		{"inputs_schema_file", &w.InputsSchemaFile},
	} {
		if raw, ok := def.Body[field.key]; ok {
			value, ok := raw.(string)
			if !ok {
				return w, fmt.Errorf("`%s` is a string", field.key)
			}
			*field.field = value
		}
	}
	if raw, ok := def.Body["auto_select"]; ok {
		value, ok := raw.(bool)
		if !ok {
			return w, fmt.Errorf("`auto_select` is a boolean")
		}
		w.AutoSelect = &value
	}
	if raw, ok := def.Body["inputs_schema"]; ok {
		schema, ok := raw.(map[string]any)
		if !ok {
			return w, fmt.Errorf("`inputs_schema` is a JSON Schema document")
		}
		w.InputsSchema = schema
	}
	// Rejected here rather than where the schema is compiled, because that is
	// `plect create` time: a contract declared twice is a load-time question,
	// and answering it at dispatch would let the ambiguity ship.
	if len(w.InputsSchema) > 0 && w.InputsSchemaFile != "" {
		return w, fmt.Errorf("inline `inputs_schema` and `inputs_schema_file` are mutually exclusive")
	}
	var err error
	if w.Display, err = valueTableFrom(def.Body, "display", lang.ClassData, pos); err != nil {
		return w, err
	}
	if w.WorkspaceProviderInputs, err = providerInputsFrom(def.Body, pos); err != nil {
		return w, err
	}
	if w.Nodes, err = workflowNodesFrom(def, pos); err != nil {
		return w, err
	}
	if w.Event.Channel, err = eventChannelsFrom(def, pos); err != nil {
		return w, err
	}
	if w.Tick, err = tickFrom(def.Body); err != nil {
		return w, err
	}
	if w.Healthcheck, err = healthcheckFrom(def.Body); err != nil {
		return w, err
	}
	return w, nil
}

func providerInputsFrom(body map[string]any, pos lang.Position) (map[string]any, error) {
	raw, ok := body["workspace_provider_inputs"]
	if !ok {
		return nil, nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`workspace_provider_inputs` is a table")
	}
	at := childPosition(pos, "workspace_provider_inputs")
	out := make(map[string]any, len(tbl))
	for key, entry := range tbl {
		value, err := lang.ParseValue(entry, lang.ClassLiteral, childPosition(at, key))
		if err != nil {
			return nil, err
		}
		out[key] = value.Literal
	}
	return out, nil
}

func workflowNodesFrom(def *lang.Definition, pos lang.Position) ([]WorkflowNode, error) {
	topology, err := lang.WorkflowNodes(def)
	if err != nil {
		return nil, err
	}
	raw := asNodeTables(def.Body["nodes"])
	if len(raw) != len(topology) {
		return nil, fmt.Errorf("internal: workflow %q declares %d nodes but the language read %d", def.ID, len(raw), len(topology))
	}
	out := make([]WorkflowNode, 0, len(topology))
	for i, node := range topology {
		at := childPosition(childPosition(pos, "nodes"), fmt.Sprintf("[%d]", i))
		inputs, err := valueTableFrom(raw[i], "inputs", lang.ClassData, at)
		if err != nil {
			return nil, err
		}
		out = append(out, WorkflowNode{
			ID:     node.ID,
			Uses:   node.Uses,
			Inputs: inputs,
			Blocks: node.Blocks,
		})
	}
	return out, nil
}

func eventChannelsFrom(def *lang.Definition, pos lang.Position) ([]EventChannel, error) {
	event, ok := def.Body["event"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw := asNodeTables(event["channel"])
	at := childPosition(pos, "event.channel")
	out := make([]EventChannel, 0, len(raw))
	names := make(map[string]bool, len(raw))
	for i, entry := range raw {
		site := childPosition(at, fmt.Sprintf("[%d]", i))
		var ch EventChannel
		for _, field := range []struct {
			key   string
			field *string
		}{
			{"name", &ch.Name},
			{"uses", &ch.Uses},
		} {
			if raw, ok := entry[field.key]; ok {
				value, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("event.channel[%d]: `%s` is a string", i, field.key)
				}
				*field.field = value
			}
		}
		if ch.Name != "" {
			if names[ch.Name] {
				return nil, fmt.Errorf("event.channel name %q is declared more than once", ch.Name)
			}
			names[ch.Name] = true
		}
		include, err := stringList(entry, "include")
		if err != nil {
			return nil, fmt.Errorf("event.channel[%d]: %w", i, err)
		}
		ch.Include = include
		inputs, err := valueTableFrom(entry, "inputs", lang.ClassData, site)
		if err != nil {
			return nil, err
		}
		ch.Inputs = inputs
		out = append(out, ch)
	}
	return out, nil
}

func tickFrom(body map[string]any) (*TickConfig, error) {
	tbl, ok := body["tick"].(map[string]any)
	if !ok {
		return nil, nil
	}
	tick := &TickConfig{}
	on, err := stringList(tbl, "on")
	if err != nil {
		return nil, fmt.Errorf("tick: %w", err)
	}
	tick.On = on
	for _, field := range []struct {
		key   string
		field *Duration
	}{
		{"heartbeat", &tick.Heartbeat},
		{"max_heartbeat", &tick.MaxHeartbeat},
	} {
		value, err := durationField(tbl, field.key)
		if err != nil {
			return nil, fmt.Errorf("tick: %w", err)
		}
		*field.field = value
	}
	return tick, nil
}

func healthcheckFrom(body map[string]any) (*HealthcheckConfig, error) {
	tbl, ok := body["healthcheck"].(map[string]any)
	if !ok {
		return nil, nil
	}
	cfg := &HealthcheckConfig{}
	for _, field := range []struct {
		key   string
		field *Duration
	}{
		{"period", &cfg.Period},
		{"stall_threshold", &cfg.StallThreshold},
	} {
		value, err := durationField(tbl, field.key)
		if err != nil {
			return nil, fmt.Errorf("healthcheck: %w", err)
		}
		*field.field = value
	}
	if raw, ok := tbl["renotify_every"]; ok {
		count, ok := raw.(int64)
		if !ok {
			return nil, fmt.Errorf("healthcheck: `renotify_every` is an integer")
		}
		cfg.RenotifyEvery = int(count)
	}
	return cfg, nil
}

func durationField(tbl map[string]any, key string) (Duration, error) {
	raw, ok := tbl[key]
	if !ok {
		return Duration{}, nil
	}
	text, ok := raw.(string)
	if !ok {
		return Duration{}, fmt.Errorf("`%s` is a duration string", key)
	}
	parsed, err := ParseDuration(text)
	if err != nil {
		return Duration{}, fmt.Errorf("`%s` %q: %w", key, text, err)
	}
	return Duration{Duration: parsed}, nil
}

func stringList(tbl map[string]any, key string) ([]string, error) {
	raw, ok := tbl[key]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("`%s` is a list of strings", key)
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		value, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("`%s` is a list of strings", key)
		}
		out = append(out, value)
	}
	return out, nil
}

// asNodeTables normalizes an array-of-tables body field, which TOML decodes
// as []map[string]any but a cascade merge rebuilds as []any.
func asNodeTables(raw any) []map[string]any {
	switch arr := raw.(type) {
	case []map[string]any:
		return arr
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, entry := range arr {
			tbl, ok := entry.(map[string]any)
			if !ok {
				return nil
			}
			out = append(out, tbl)
		}
		return out
	}
	return nil
}
