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
// JSON-Schema document tasks/providers carry, because a channel's inputs are
// rendered template strings and only their presence is checked before delivery.
type ChannelInputSpec struct {
	Type     string `toml:"type"`
	Required bool   `toml:"required"`
}

// ChannelDefinition binds a workflow's [[event.channel]] to a built-in delivery
// primitive. It follows the provider trust model, not the per-workdir workflow
// cascade: an `exec` channel runs argv directly and a unix_socket channel writes
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
	Timeout Duration `toml:"timeout"`
	// Execution selects the execution plane for an exec channel: "host"
	// (default) or "environment". unix_socket has no argv to place in a
	// plane, so declaring it there is a load error.
	Execution string `toml:"execution"`
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
		if d.Execution != "" {
			return fmt.Errorf("unix_socket channel must not set `execution` (it has no argv to place in a plane)")
		}
	case ChannelTypeExec:
		if d.Command == "" || len(d.Args) == 0 {
			return fmt.Errorf("exec channel requires `command` and at least one `args` entry")
		}
		if d.Path != "" || d.Body != "" {
			return fmt.Errorf("exec channel must not set unix_socket fields (`path`/`body`)")
		}
		if d.Execution != "" && d.Execution != ExecutionHost && d.Execution != ExecutionEnvironment {
			return fmt.Errorf("exec channel `execution` must be %q or %q, got %q", ExecutionHost, ExecutionEnvironment, d.Execution)
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
	}
	return nil
}

// LoadChannels loads `channels/*.toml` from plugin + global layers only. The
// per-workdir cascade is excluded for the same reason as providers — a channel
// may run argv (see ChannelDefinition).
func (c *Config) LoadChannels() (map[string]ChannelDefinition, error) {
	out := make(map[string]ChannelDefinition)
	var dirs []string
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, filepath.Join(plugin, "channels"))
	}
	if c.BaseDir != "" {
		dirs = append(dirs, filepath.Join(c.BaseDir, "channels"))
	}
	for _, dir := range dirs {
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			d, err := loadChannelFile(path)
			if err != nil {
				return nil, fmt.Errorf("channel %s: %w", path, err)
			}
			out[d.ID] = d
		}
	}
	return out, nil
}

func loadChannelFile(path string) (ChannelDefinition, error) {
	stem, err := validateStem(path, workflowStemRE, "channel")
	if err != nil {
		return ChannelDefinition{}, err
	}
	var d ChannelDefinition
	if _, err := toml.DecodeFile(path, &d); err != nil {
		return d, err
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
