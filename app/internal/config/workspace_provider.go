package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// WorkspaceProviderConfig is loaded from `workspaces/<id>.toml` in the
// trusted base layers (plugin dirs + global config). A workspace provider
// owns everything about a *kind of resource*'s workspace lifecycle on this
// machine — knowledge that is independent of any particular workflow:
//
//   - how resource identifiers map to session ids (the resolver: pure
//     regex + template, offline)
//   - how the session workspace is acquired and released for such a resource
//     (setup/cleanup — the @workflow pseudo-node scripts)
//   - the outputs contract those scripts expose (outputs_schema, incl. which
//     keys may be explicitly updated through trusted side paths)
//
// Workflows reference a workspace provider by id
// (`workspace_provider = "<id>"`) and own the task shape on top of it: task
// nodes, inputs, done_when, display.
//
// Workspace providers deliberately do NOT participate in the per-workspace-dir
// cascade: setup must be resolvable before any workspace exists, and the
// scripts are arbitrary shell, so only user/machine-owned layers may supply
// them. Same-id files in deeper layers win (global overrides plugin),
// mirroring task definitions — a setup/cleanup pair is atomic, so
// "append" has no sensible meaning.
type WorkspaceProviderConfig struct {
	ID string `toml:"-"`
	// Match / Name form the optional resolver: a regex with named captures
	// plus a template over those captures producing the session id. Both or
	// neither must be set. Workspace providers without a resolver serve
	// identity workflows (the input string IS the session id) and do not
	// participate in auto-dispatch.
	Match string `toml:"match"`
	Name  string `toml:"name"`
	// Setup acquires the session workspace: it MUST emit a JSON object with
	// the reserved `workspace_dir` key on stdout. Required — acquisition is
	// the workspace provider's reason to exist.
	Setup string `toml:"setup"`
	// Cleanup releases whatever setup acquired. It intentionally does NOT
	// delete workspace_dir unless the script says so (setup/cleanup symmetry
	// is the author's contract).
	Cleanup string `toml:"cleanup"`
	// Subscribe binds a session to a resource of this kind at runtime (the
	// `plect subscribe` verb), the counterpart to the dispatch-time
	// auto-subscribe task. Optional: a workspace provider without it cannot
	// be subscribed to after dispatch. The hook's template surface is the
	// current session (.SessionName) and the opaque resource (.ResourceID) —
	// everything resource-specific (a watcher registry, say) stays here.
	Subscribe string `toml:"subscribe"`
	// OutputsSchema is the @workflow pseudo-node contract: what setup emits and
	// which keys trusted side paths may explicitly update (`mutable = true`).
	// `workspace_dir` is reserved always-immutable.
	OutputsSchema     map[string]any `toml:"outputs_schema"`
	OutputsSchemaFile string         `toml:"outputs_schema_file"`
	BaseDir           string         `toml:"-"`
	SourcePath        string         `toml:"-"`
}

// ResolvedOutputsSchemaPath joins OutputsSchemaFile with BaseDir.
func (p WorkspaceProviderConfig) ResolvedOutputsSchemaPath() string {
	return resolveSchemaPath(p.OutputsSchemaFile, p.BaseDir)
}

// HasResolver reports whether the workspace provider declares the
// resource-id → session-id transform.
func (p WorkspaceProviderConfig) HasResolver() bool {
	return p.Match != ""
}

// LoadWorkspaceProviders loads `workspaces/*.toml` from the trusted base
// layers only: plugin dirs first, then the global config dir; the global
// layer's same-id file replaces a plugin layer's, but two plugin layers
// declaring the same id is a load error (see loadTrustedLayer). The
// per-workspace-dir ancestor cascade is deliberately excluded (see
// WorkspaceProviderConfig).
func (c *Config) LoadWorkspaceProviders() (map[string]WorkspaceProviderConfig, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "config", "workspaces"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "workspaces")
	}
	out, err := loadTrustedLayer(pluginDirs, globalDir, func(path string) (WorkspaceProviderConfig, error) {
		p, err := loadWorkspaceProviderFile(path)
		if err != nil {
			return WorkspaceProviderConfig{}, fmt.Errorf("workspace provider %s: %w", path, err)
		}
		return p, nil
	}, func(p WorkspaceProviderConfig) string { return p.ID })
	if err != nil {
		return nil, err
	}
	var hooks []hookSource
	for _, p := range out {
		hooks = append(hooks,
			hookSource{desc: fmt.Sprintf("workspace provider %q setup", p.ID), sourcePath: p.SourcePath, script: p.Setup},
			hookSource{desc: fmt.Sprintf("workspace provider %q cleanup", p.ID), sourcePath: p.SourcePath, script: p.Cleanup},
			hookSource{desc: fmt.Sprintf("workspace provider %q subscribe", p.ID), sourcePath: p.SourcePath, script: p.Subscribe},
		)
	}
	if err := checkBinRefs(hooks, c.Plugins, c.catalogRegistrations, c.catalogLock, c.catalogCacheRoot); err != nil {
		return nil, err
	}
	return out, nil
}

func loadWorkspaceProviderFile(path string) (WorkspaceProviderConfig, error) {
	stem, err := validateStem(path, workflowStemRE, "workspace provider")
	if err != nil {
		return WorkspaceProviderConfig{}, err
	}
	var p WorkspaceProviderConfig
	if _, err := toml.DecodeFile(path, &p); err != nil {
		return p, err
	}
	p.ID = stem
	p.SourcePath = path
	p.BaseDir = configFileDir(path)
	if p.Setup == "" {
		return p, fmt.Errorf("`setup` is required: a workspace provider's purpose is acquiring the workspace")
	}
	if (p.Match == "") != (p.Name == "") {
		return p, fmt.Errorf("`match` and `name` must be declared together (the resolver pair) or not at all")
	}
	if err := rejectMutableWorkspaceDir(p.OutputsSchema, p.ResolvedOutputsSchemaPath()); err != nil {
		return p, err
	}
	return p, nil
}

// rejectMutableWorkspaceDir fails the load when an outputs schema declares
// the reserved `workspace_dir` key mutable — cleanup correctness depends on
// it, so no external updater may ever rewrite it. Duplicated from the task
// package's MutableOutputKeys check (config cannot import task) so the error
// fires at load time rather than first set-output.
func rejectMutableWorkspaceDir(inline map[string]any, filePath string) error {
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
	if prop, ok := props["workspace_dir"].(map[string]any); ok {
		if b, ok := prop["mutable"].(bool); ok && b {
			return fmt.Errorf("output key \"workspace_dir\" is reserved and always immutable; remove `mutable = true`")
		}
	}
	return nil
}
