package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

const (
	ChannelTypeUnixSocket = "unix_socket"
	ChannelTypeExec       = "exec"
	ChannelTypeShell      = "shell"
)

// ChannelInputSpec is the per-key shorthand in a channel's [input_schema]
// (`path = { type = "string", required = true }`) — deliberately not the full
// JSON-Schema document effects and workspace providers carry, because only a
// channel input's presence is checked before delivery.
type ChannelInputSpec struct {
	Type     string
	Required bool
	// Default is the value delivery uses when the referencing
	// [[event.channel]] sets no such input. Without it an optional input is
	// unusable: a projection of an unset key fails delivery rather than
	// resolving to empty.
	Default string
	// HasDefault distinguishes a declared `default = ""` from no default at
	// all. Emptiness is a usable default — it is what lets a channel pass a
	// flag whose value is legitimately empty — so the two cannot be folded
	// into one.
	HasDefault bool
}

// ChannelDefinition binds a workflow's [[event.channel]] to one delivery
// primitive. It follows the workspace provider trust model, not the
// per-workspace-dir workflow cascade: an exec or shell channel runs a
// process, so only user/machine-owned layers may declare one and event data
// must never choose what runs.
type ChannelDefinition struct {
	ID          string
	Type        string
	InputSchema map[string]ChannelInputSpec

	// Path and Body are the unix_socket primitive's dial target and framed
	// payload; nil for a process delivery.
	Path *lang.Value
	Body *lang.Value
	// Action is the process an exec or shell channel runs; nil for
	// unix_socket. A channel's own `type` is that action's type.
	Action *lang.Action
	// Timeout bounds a single delivery attempt. Nil means the caller's retry
	// policy decides.
	Timeout *lang.Value

	SourcePath string
	// FromPlugin says a plugin layer wrote this definition, which is what
	// decides whether its bin references may name another plugin.
	FromPlugin bool
}

// Ownership names the layer that wrote this definition, for the reference
// rules that differ between shipped and user-authored config.
func (d ChannelDefinition) Ownership() lang.Ownership {
	return lang.Ownership{IsPlugin: d.FromPlugin}
}

// ApplyInputDefaults returns inputs with every [input_schema] default filled
// in for a key the caller left unset, so a definition may reference an
// author-declared optional parameter without every workflow wiring it.
func (d ChannelDefinition) ApplyInputDefaults(inputs map[string]any) map[string]any {
	out := make(map[string]any, len(inputs)+len(d.InputSchema))
	for k, v := range inputs {
		out[k] = v
	}
	for key, spec := range d.InputSchema {
		if !spec.HasDefault {
			continue
		}
		if _, set := out[key]; !set {
			out[key] = spec.Default
		}
	}
	return out
}

// LoadChannels loads the channels declared under `channels/` in the trusted
// base layers only: plugin dirs first, then the global config dir. The
// per-workspace-dir cascade is excluded for the same reason as workspace
// providers — a channel may run a process. Discovery is directory-scoped for
// the same transitional reason LoadResourceDefs states.
func (c *Config) LoadChannels() (map[string]ChannelDefinition, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "config", "channels"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "channels")
	}
	return loadTrustedLayer(pluginDirs, globalDir, c.loadChannelDocument, func(d ChannelDefinition) string { return d.ID })
}

func (c *Config) loadChannelDocument(path string, fromPlugin bool) ([]ChannelDefinition, error) {
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
	out := make([]ChannelDefinition, 0, len(parsed))
	for _, def := range parsed {
		if def.Kind != lang.KindChannel {
			return nil, fmt.Errorf("%s: %q declares kind %q; a definition under channels/ is a channel", path, def.ID, def.Kind)
		}
		if err := validation.ValidateDefinition(def); err != nil {
			return nil, err
		}
		channel, err := channelDefinitionFrom(def, path, fromPlugin)
		if err != nil {
			return nil, fmt.Errorf("channel %s in %s: %w", def.ID, path, err)
		}
		out = append(out, channel)
	}
	return out, nil
}

// channelDefinitionFrom reads the fields delivery needs off a validated
// declaration. The input_schema shorthand's own rules stay here rather than
// in the language validator, for the reason resourceDefFrom states.
func channelDefinitionFrom(def *lang.Definition, path string, fromPlugin bool) (ChannelDefinition, error) {
	pos := lang.Position{File: path, Path: def.ID}
	kind, _ := def.Body["type"].(string)
	d := ChannelDefinition{
		ID:         def.ID,
		Type:       kind,
		SourcePath: path,
		FromPlugin: fromPlugin,
	}
	switch kind {
	case ChannelTypeUnixSocket:
		var err error
		if d.Path, err = lang.ParseValue(def.Body["path"], lang.ClassData, childPosition(pos, "path")); err != nil {
			return d, err
		}
		if d.Body, err = lang.ParseValue(def.Body["body"], lang.ClassData, childPosition(pos, "body")); err != nil {
			return d, err
		}
	case ChannelTypeExec, ChannelTypeShell:
		action, err := lang.ChannelAction(def, pos)
		if err != nil {
			return d, err
		}
		d.Action = action
	}
	if raw, ok := def.Body["timeout"]; ok {
		timeout, err := lang.ParseValue(raw, lang.ClassData, childPosition(pos, "timeout"))
		if err != nil {
			return d, err
		}
		// A literal deadline is parsed at load so a typo fails here rather
		// than on the first event this channel is asked to deliver.
		if timeout.Form == lang.FormLiteral {
			literal, ok := timeout.Literal.(string)
			if !ok {
				return d, fmt.Errorf("`timeout` is a duration string")
			}
			if _, err := ParseDuration(literal); err != nil {
				return d, fmt.Errorf("`timeout` %q: %w", literal, err)
			}
		}
		d.Timeout = timeout
	}
	schema, err := channelInputSchema(def.Body["input_schema"])
	if err != nil {
		return d, err
	}
	d.InputSchema = schema
	return d, nil
}

func childPosition(pos lang.Position, segment string) lang.Position {
	return lang.Position{File: pos.File, Path: pos.Path + "." + segment}
}

func channelInputSchema(raw any) (map[string]ChannelInputSpec, error) {
	if raw == nil {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("`input_schema` is a table of parameter specs")
	}
	out := make(map[string]ChannelInputSpec, len(table))
	for key, entry := range table {
		if !channelInputName.MatchString(key) {
			return nil, fmt.Errorf("input_schema %q: a parameter name matches %s", key, channelInputName)
		}
		spec, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input_schema %q: expected a table", key)
		}
		var parsed ChannelInputSpec
		var declaredType bool
		for _, field := range sortedSpecKeys(spec) {
			value := spec[field]
			var ok bool
			switch field {
			case "type":
				parsed.Type, ok = value.(string)
				declaredType = true
			case "required":
				parsed.Required, ok = value.(bool)
			case "default":
				parsed.Default, ok = value.(string)
				parsed.HasDefault = true
			default:
				return nil, fmt.Errorf("input_schema %q: %q is not part of a parameter spec", key, field)
			}
			// A wrong type here is refused rather than dropped: a
			// `required = "true"` string read as the boolean's zero value
			// would silently stop gating, and a non-string default would
			// silently leave the parameter unusable.
			if !ok {
				return nil, fmt.Errorf("input_schema %q: %q is %s, not %T", key, field, specFieldType(field), value)
			}
		}
		if !declaredType {
			return nil, fmt.Errorf("input_schema %q: `type` is required", key)
		}
		if parsed.Type != "string" {
			return nil, fmt.Errorf("input_schema %q: `type` is \"string\"; a channel parameter carries no other type", key)
		}
		if parsed.Required && parsed.HasDefault {
			return nil, fmt.Errorf("input_schema %q: `required` and `default` are mutually exclusive", key)
		}
		out[key] = parsed
	}
	return out, nil
}

// channelInputName is the parameter-name grammar: a projection reads a
// parameter as `inputs.<key>`, so a key that is not a bare path segment is
// unreadable rather than merely unconventional.
var channelInputName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func specFieldType(field string) string {
	if field == "required" {
		return "a boolean"
	}
	return "a string"
}

// sortedSpecKeys walks a parameter spec in a fixed order, so a spec breaking
// more than one rule reports the same error on every run.
func sortedSpecKeys(spec map[string]any) []string {
	keys := make([]string, 0, len(spec))
	for key := range spec {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ValidateWorkflowChannels checks each [[event.channel]] resolves and its inputs
// satisfy the definition's input_schema. Inputs are still template strings here,
// so a value referencing a missing node output surfaces at delivery, not now.
func ValidateWorkflowChannels(wf WorkflowFile, defs map[string]ChannelDefinition) error {
	seen := make(map[string]bool, len(wf.Event.Channel))
	for i, ch := range wf.Event.Channel {
		if ch.Name == "" {
			return fmt.Errorf("event.channel[%d]: `name` is required", i)
		}
		if seen[ch.Name] {
			return fmt.Errorf("event.channel name %q is declared more than once", ch.Name)
		}
		seen[ch.Name] = true
		if ch.Uses == "" {
			return fmt.Errorf("event.channel %q: `uses` is required", ch.Name)
		}
		def, ok := defs[ch.Uses]
		if !ok {
			return fmt.Errorf("event.channel %q: uses unknown channel definition %q", ch.Name, ch.Uses)
		}
		for key := range ch.Inputs {
			if _, declared := def.InputSchema[key]; !declared {
				return fmt.Errorf("event.channel %q: input %q is not declared in channel %q input_schema", ch.Name, key, ch.Uses)
			}
		}
		for key, spec := range def.InputSchema {
			if spec.Required {
				if _, set := ch.Inputs[key]; !set {
					return fmt.Errorf("event.channel %q: required input %q (channel %q) is not set", ch.Name, key, ch.Uses)
				}
			}
		}
		if len(ch.Include) == 0 {
			return fmt.Errorf("event.channel %q: `include` must list at least one event type glob", ch.Name)
		}
		if slices.Contains(ch.Include, "") {
			return fmt.Errorf("event.channel %q: `include` contains an empty glob", ch.Name)
		}
		for _, include := range ch.Include {
			if strings.HasPrefix(include, "meta:") {
				return fmt.Errorf("event.channel %q: `include` metadata selectors are no longer supported; use event type globs", ch.Name)
			}
		}
	}
	return nil
}
