package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	ChannelTypeUnixSocket = "unix_socket"
	ChannelTypeExec       = "exec"
)

// ChannelInputSpec is the per-key shorthand in a channel's [input_schema]
// (`path = { type = "string", required = true }`) — deliberately not the full
// JSON-Schema document tasks/workspaces carry, because a channel's inputs are
// rendered template strings and only their presence is checked before delivery.
type ChannelInputSpec struct {
	Type     string `toml:"type"`
	Required bool   `toml:"required"`
	// Default is the value delivery uses when the referencing
	// [[event.channel]] sets no such input. Without it an optional input is
	// unusable: rendering runs under missingkey=error, so a definition
	// referencing an unset key fails delivery rather than rendering empty.
	// An empty string means no default was declared.
	Default string `toml:"default"`
}

// ChannelDefinition binds a workflow's [[event.channel]] to a built-in delivery
// primitive. It follows the workspace provider trust model, not the
// per-workspace-dir workflow cascade: an `exec` channel runs argv directly
// and a unix_socket channel writes
// to a socket, so only user/machine-owned layers may declare one and event data
// must never choose `uses`/`command`. The primitive fields (path/body,
// command/args) are templates rendered against {.Event, .Inputs} at delivery.
type ChannelDefinition struct {
	ID          string                      `toml:"-"`
	Type        string                      `toml:"type"`
	InputSchema map[string]ChannelInputSpec `toml:"input_schema"`

	Path string `toml:"path"` // unix_socket: dial target
	Body string `toml:"body"` // unix_socket: framed payload

	Command string   `toml:"command"` // exec: argv[0]
	Args    []string `toml:"args"`    // exec: argv[1:]
	// Timeout bounds a single delivery attempt — a shared deadline applied by
	// whichever primitive supports one (exec today; a socket write later).
	// It is a template over `.Inputs` so an author can expose the deadline as
	// a declared parameter; a literal like "5s" is simply a template with no
	// actions. Empty means the caller's retry policy decides.
	Timeout string `toml:"timeout"`
}

// TimeoutIsTemplate reports whether Timeout defers to a channel input rather
// than naming a duration outright.
func (d ChannelDefinition) TimeoutIsTemplate() bool {
	return strings.Contains(d.Timeout, "{{")
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
		if spec.Default == "" {
			continue
		}
		if _, set := out[key]; !set {
			out[key] = spec.Default
		}
	}
	return out
}

// Validate rejects the other primitive's fields so a typo in `type` surfaces
// instead of silently picking up half a config. Template correctness is
// exercised at delivery, not here.
func (d ChannelDefinition) Validate() error {
	switch d.Type {
	case ChannelTypeUnixSocket:
		if d.Path == "" || d.Body == "" {
			return fmt.Errorf("unix_socket channel requires `path` and `body`")
		}
		if d.Command != "" || len(d.Args) > 0 {
			return fmt.Errorf("unix_socket channel must not set exec fields (`command`/`args`)")
		}
	case ChannelTypeExec:
		if d.Command == "" || len(d.Args) == 0 {
			return fmt.Errorf("exec channel requires `command` and at least one `args` entry")
		}
		if d.Path != "" || d.Body != "" {
			return fmt.Errorf("exec channel must not set unix_socket fields (`path`/`body`)")
		}
	case "":
		return fmt.Errorf("channel `type` is required (%q or %q)", ChannelTypeUnixSocket, ChannelTypeExec)
	default:
		return fmt.Errorf("unknown channel type %q (want %q or %q)", d.Type, ChannelTypeUnixSocket, ChannelTypeExec)
	}
	for key, spec := range d.InputSchema {
		if spec.Type == "" {
			return fmt.Errorf("input_schema %q: `type` is required", key)
		}
		if spec.Required && spec.Default != "" {
			return fmt.Errorf("input_schema %q: `required` and `default` are mutually exclusive", key)
		}
	}
	if d.Timeout != "" && !d.TimeoutIsTemplate() {
		if _, err := ParseDuration(d.Timeout); err != nil {
			return fmt.Errorf("`timeout` %q: %w", d.Timeout, err)
		}
	}
	return nil
}

// LoadChannels loads `channels/*.toml` from plugin + global layers only. The
// per-workspace-dir cascade is excluded for the same reason as workspace
// providers — a channel may run argv (see ChannelDefinition). The global
// layer's same-id file
// replaces a plugin layer's, but two plugin layers declaring the same id is
// a load error (see loadTrustedLayer).
func (c *Config) LoadChannels() (map[string]ChannelDefinition, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "config", "channels"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "channels")
	}
	return loadTrustedLayer(pluginDirs, globalDir, func(path string) (ChannelDefinition, error) {
		d, err := loadChannelFile(path)
		if err != nil {
			return ChannelDefinition{}, fmt.Errorf("channel %s: %w", path, err)
		}
		return d, nil
	}, func(d ChannelDefinition) string { return d.ID })
}

func loadChannelFile(path string) (ChannelDefinition, error) {
	stem, err := validateStem(path, workflowStemRE, "channel")
	if err != nil {
		return ChannelDefinition{}, err
	}
	var d ChannelDefinition
	md, err := toml.DecodeFile(path, &d)
	if err != nil {
		return d, err
	}
	for _, key := range md.Undecoded() {
		if len(key) == 1 && key[0] == "execution" {
			return d, fmt.Errorf("`execution` is retired along with the environment execution plane; see docs/migrations/")
		}
	}
	d.ID = stem
	if err := d.Validate(); err != nil {
		return d, err
	}
	return d, nil
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
