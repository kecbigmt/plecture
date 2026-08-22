package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// carriedFields are the task-document fields an effect declaration still
// carries while the surfaces move one at a time. They are decoded here, by
// the runtime loader, and withheld from the language validator: the ratified
// effect surface must not certify them as valid, so `lang` never sees them.
//
// The PR that introduces task documents deletes this type, its decode, and
// the withholding below along with the four fields themselves.
type carriedFields struct {
	DoneWhen       *DoneWhen         `toml:"done_when"`
	Requires       []string          `toml:"requires"`
	DynamicOutputs []DynamicOutput   `toml:"outputs"`
	Chains         []ChainDefinition `toml:"chains"`
}

var carriedFieldNames = []string{"done_when", "requires", "outputs", "chains"}

// loadEffectDocument reads every `kind = "effect"` declaration in one
// definition document.
func (c *Config) loadEffectDocument(path string, fromPlugin bool) ([]TaskDefinition, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := lang.ParseDefinitionDocument(path, src)
	if err != nil {
		return nil, err
	}
	validation := lang.Validation{
		From:        lang.Ownership{IsPlugin: fromPlugin},
		Executables: c.binResolver(path),
	}
	out := make([]TaskDefinition, 0, len(parsed))
	for _, def := range parsed {
		if def.Kind != lang.KindEffect {
			return nil, fmt.Errorf("%s: %q declares kind %q; a definition under tasks/ is an effect", path, def.ID, def.Kind)
		}
		carried, err := decodeCarriedFields(def)
		if err != nil {
			return nil, fmt.Errorf("effect %s in %s: %w", def.ID, path, err)
		}
		if err := validation.ValidateDefinition(withoutCarriedFields(def)); err != nil {
			return nil, err
		}
		effect, err := effectFrom(def, carried, path, fromPlugin)
		if err != nil {
			return nil, fmt.Errorf("effect %s in %s: %w", def.ID, path, err)
		}
		out = append(out, effect)
	}
	return out, nil
}

// withoutCarriedFields hands the validator the effect surface alone. The
// `outputs` key is shared: a table is the nesting joint and belongs to the
// surface, while an array of tables is the carried dynamic-output list and
// does not.
func withoutCarriedFields(def *lang.Definition) *lang.Definition {
	trimmed := *def
	trimmed.Body = make(map[string]any, len(def.Body))
	for key, value := range def.Body {
		trimmed.Body[key] = value
	}
	for _, name := range carriedFieldNames {
		if name == "outputs" && !isDynamicOutputList(def.Body[name]) {
			continue
		}
		delete(trimmed.Body, name)
	}
	return &trimmed
}

func isDynamicOutputList(raw any) bool {
	switch raw.(type) {
	case []map[string]any, []any:
		return true
	}
	return false
}

// decodeCarriedFields re-encodes the carried subtree and decodes it with the
// typed decoders that already own those fields' shape and validation, rather
// than duplicating them against the parsed tree.
func decodeCarriedFields(def *lang.Definition) (carriedFields, error) {
	subtree := map[string]any{}
	for _, name := range carriedFieldNames {
		raw, ok := def.Body[name]
		if !ok {
			continue
		}
		if name == "outputs" && !isDynamicOutputList(raw) {
			continue
		}
		subtree[name] = raw
	}
	var carried carriedFields
	if len(subtree) == 0 {
		return carried, nil
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(subtree); err != nil {
		return carried, err
	}
	if _, err := toml.Decode(encoded.String(), &carried); err != nil {
		return carried, err
	}
	return carried, nil
}

// effectFrom reads the fields the runtime needs off a validated declaration.
func effectFrom(def *lang.Definition, carried carriedFields, path string, fromPlugin bool) (TaskDefinition, error) {
	pos := lang.Position{File: path, Path: def.ID}
	d := TaskDefinition{
		ID:             def.ID,
		SourcePath:     path,
		BaseDir:        configFileDir(path),
		FromPlugin:     fromPlugin,
		DoneWhen:       carried.DoneWhen,
		Requires:       carried.Requires,
		DynamicOutputs: carried.DynamicOutputs,
		Chains:         carried.Chains,
	}
	if raw, ok := def.Body["scope"]; ok {
		scope, ok := raw.(string)
		if !ok {
			return d, fmt.Errorf("`scope` is a string")
		}
		d.Scope = scope
	}
	var err error
	if d.Setup, err = actionField(def, path, "setup"); err != nil {
		return d, err
	}
	if d.Cleanup, err = actionField(def, path, "cleanup"); err != nil {
		return d, err
	}
	if d.Health, err = healthFrom(def, path); err != nil {
		return d, err
	}
	if err := d.Health.Validate(); err != nil {
		return d, err
	}
	if d.Terminal, err = terminalFrom(def, path); err != nil {
		return d, err
	}
	if !d.Terminal.IsDeclared() {
		// A bare `[terminal]` header with every verb left out carries no
		// obligation, and must not read as "this effect owns the plan's
		// interactive endpoint" downstream.
		d.Terminal = nil
	}
	if err := d.readInner(def, pos); err != nil {
		return d, err
	}
	if err := d.readOutputsBind(def, pos); err != nil {
		return d, err
	}
	for _, field := range []struct {
		key    string
		schema *map[string]any
		file   *string
	}{
		{"inputs_schema", &d.InputsSchema, &d.InputsSchemaFile},
		{"outputs_schema", &d.OutputsSchema, &d.OutputsSchemaFile},
		{"locals_schema", &d.LocalsSchema, &d.LocalsSchemaFile},
	} {
		if raw, ok := def.Body[field.key]; ok {
			schema, ok := raw.(map[string]any)
			if !ok {
				return d, fmt.Errorf("`%s` is a JSON Schema document", field.key)
			}
			*field.schema = schema
		}
		if raw, ok := def.Body[field.key+"_file"]; ok {
			file, ok := raw.(string)
			if !ok {
				return d, fmt.Errorf("`%s_file` is a path", field.key)
			}
			*field.file = file
		}
	}
	return d, nil
}

func (d *TaskDefinition) readInner(def *lang.Definition, pos lang.Position) error {
	raw, ok := def.Body["inner"]
	if !ok {
		return nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("`inner` is a table")
	}
	uses, ok := tbl["uses"].(string)
	if !ok {
		return fmt.Errorf("`inner` names the effect it wraps through `uses`")
	}
	d.Inner = uses
	var err error
	at := childPosition(pos, "inner")
	if d.InnerInputs, err = valueTableFrom(tbl, "inputs", at); err != nil {
		return err
	}
	if d.InnerEnv, err = valueTableFrom(tbl, "env", at); err != nil {
		return err
	}
	return nil
}

func (d *TaskDefinition) readOutputsBind(def *lang.Definition, pos lang.Position) error {
	raw, ok := def.Body["outputs"]
	if !ok || isDynamicOutputList(raw) {
		return nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("`outputs` is a table")
	}
	bind, err := valueTableFrom(tbl, "bind", childPosition(pos, "outputs"))
	if err != nil {
		return err
	}
	d.OutputsBind = bind
	return nil
}

func valueTableFrom(body map[string]any, field string, pos lang.Position) (map[string]*lang.Value, error) {
	raw, ok := body[field]
	if !ok {
		return nil, nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`%s` is a table", field)
	}
	at := childPosition(pos, field)
	out := make(map[string]*lang.Value, len(tbl))
	for key, entry := range tbl {
		value, err := lang.ParseValue(entry, lang.ClassBinding, childPosition(at, key))
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func healthFrom(def *lang.Definition, path string) (*HealthConfig, error) {
	raw, ok := def.Body["health"]
	if !ok {
		return nil, nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`health` is a table")
	}
	health := &HealthConfig{}
	for _, probe := range []struct {
		name   string
		target **lang.Action
	}{
		{"alive", &health.Alive},
		{"activity", &health.Activity},
	} {
		action, err := actionIn(tbl, probe.name, path, def.ID+".health")
		if err != nil {
			return nil, err
		}
		*probe.target = action
	}
	return health, nil
}

func terminalFrom(def *lang.Definition, path string) (*TerminalConfig, error) {
	raw, ok := def.Body["terminal"]
	if !ok {
		return nil, nil
	}
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`terminal` is a table")
	}
	terminal := &TerminalConfig{}
	for _, verb := range []struct {
		name   string
		target **lang.Action
	}{
		{"attach", &terminal.Attach},
		{"capture", &terminal.Capture},
		{"send_text", &terminal.SendText},
		{"send_keys", &terminal.SendKeys},
	} {
		action, err := actionIn(tbl, verb.name, path, def.ID+".terminal")
		if err != nil {
			return nil, err
		}
		*verb.target = action
	}
	return terminal, nil
}

func actionIn(body map[string]any, field, path, tablePath string) (*lang.Action, error) {
	raw, ok := body[field]
	if !ok {
		return nil, nil
	}
	return lang.ParseAction(raw, lang.Position{File: path, Path: tablePath + "." + field})
}
