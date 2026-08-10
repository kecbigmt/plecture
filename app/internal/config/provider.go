package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ProviderConfig is loaded from `providers/<id>.toml` in the trusted base
// layers (plugin dirs + global config). A provider owns everything about a
// *kind of resource* on this machine — knowledge that is independent of any
// particular workflow:
//
//   - how resource identifiers map to session ids (the resolver: pure
//     regex + template, offline)
//   - how a working directory is acquired and released for such a resource
//     (setup/cleanup — the @workflow pseudo-node scripts)
//   - the outputs contract those scripts expose (outputs_schema, incl. which
//     keys may be explicitly updated through trusted side paths)
//
// Workflows reference a provider by id (`provider = "<id>"`) and own the
// task shape on top of it: task nodes, inputs, done_when, display.
//
// Providers deliberately do NOT participate in the per-worktree cascade:
// setup must be resolvable before any working directory exists, and the
// scripts are arbitrary shell, so only user/machine-owned layers may supply
// them. Same-id files in deeper layers win (global overrides plugin),
// mirroring task definitions — a setup/cleanup pair is atomic, so
// "append" has no sensible meaning.
type ProviderConfig struct {
	ID string `toml:"-"`
	// Match / Name form the optional resolver: a regex with named captures
	// plus a template over those captures producing the session id. Both or
	// neither must be set. Providers without a resolver serve identity
	// workflows (the input string IS the session id) and do not participate
	// in auto-dispatch.
	Match string `toml:"match"`
	Name  string `toml:"name"`
	// Setup acquires the working directory: it MUST emit a JSON object with
	// the reserved `workdir` key on stdout. Required — acquisition is the
	// provider's reason to exist.
	Setup string `toml:"setup"`
	// Cleanup releases whatever setup acquired. It intentionally does NOT
	// delete workdir unless the script says so (setup/cleanup symmetry is
	// the author's contract).
	Cleanup string `toml:"cleanup"`
	// Subscribe binds a session to a resource of this kind at runtime (the
	// `sennit subscribe` verb), the counterpart to the dispatch-time
	// auto-subscribe task. Optional: a provider without it cannot be
	// subscribed to after dispatch. The hook's template surface is the
	// current session (.SessionName) and the opaque resource (.ResourceID) —
	// everything resource-specific (a watcher registry, say) stays here.
	Subscribe string `toml:"subscribe"`
	// OutputsSchema is the @workflow pseudo-node contract: what setup emits and
	// which keys trusted side paths may explicitly update (`mutable = true`).
	// `workdir` is reserved always-immutable.
	OutputsSchema     map[string]any `toml:"outputs_schema"`
	OutputsSchemaFile string         `toml:"outputs_schema_file"`
	BaseDir           string         `toml:"-"`
	SourcePath        string         `toml:"-"`
}

// ResolvedOutputsSchemaPath joins OutputsSchemaFile with BaseDir.
func (p ProviderConfig) ResolvedOutputsSchemaPath() string {
	return resolveSchemaPath(p.OutputsSchemaFile, p.BaseDir)
}

// HasResolver reports whether the provider declares the resource-id →
// session-id transform.
func (p ProviderConfig) HasResolver() bool {
	return p.Match != ""
}

// LoadProviders loads `providers/*.toml` from the trusted base layers only:
// plugin dirs first, then the global config dir; a deeper layer's same-id
// file replaces the shallower one. The per-worktree ancestor cascade is
// deliberately excluded (see ProviderConfig).
func (c *Config) LoadProviders() (map[string]ProviderConfig, error) {
	out := make(map[string]ProviderConfig)
	var dirs []string
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, filepath.Join(plugin, "providers"))
	}
	if c.BaseDir != "" {
		dirs = append(dirs, filepath.Join(c.BaseDir, "providers"))
	}
	for _, dir := range dirs {
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			p, err := loadProviderFile(path)
			if err != nil {
				return nil, fmt.Errorf("provider %s: %w", path, err)
			}
			out[p.ID] = p
		}
	}
	return out, nil
}

func loadProviderFile(path string) (ProviderConfig, error) {
	stem, err := validateStem(path, workflowStemRE, "provider")
	if err != nil {
		return ProviderConfig{}, err
	}
	var p ProviderConfig
	if _, err := toml.DecodeFile(path, &p); err != nil {
		return p, err
	}
	p.ID = stem
	p.SourcePath = path
	p.BaseDir = configFileDir(path)
	if p.Setup == "" {
		return p, fmt.Errorf("`setup` is required: a provider's purpose is acquiring the working directory")
	}
	if (p.Match == "") != (p.Name == "") {
		return p, fmt.Errorf("`match` and `name` must be declared together (the resolver pair) or not at all")
	}
	if err := rejectMutableWorkdir(p.OutputsSchema, p.ResolvedOutputsSchemaPath()); err != nil {
		return p, err
	}
	return p, nil
}

// rejectMutableWorkdir fails the load when an outputs schema declares the
// reserved `workdir` key mutable — cleanup correctness depends on it, so no
// external updater may ever rewrite it. Duplicated from the task package's
// MutableOutputKeys check (config cannot import task) so the error fires
// at load time rather than first set-output.
func rejectMutableWorkdir(inline map[string]any, filePath string) error {
	raw := inline
	if len(raw) == 0 && filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", filePath, err)
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", filePath, err)
		}
	}
	props, _ := raw["properties"].(map[string]any)
	if prop, ok := props["workdir"].(map[string]any); ok {
		if b, ok := prop["mutable"].(bool); ok && b {
			return fmt.Errorf("output key \"workdir\" is reserved and always immutable; remove `mutable = true`")
		}
	}
	return nil
}
