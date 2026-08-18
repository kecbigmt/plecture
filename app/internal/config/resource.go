package config

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// ResourceDef is loaded from `resources/<id>.toml` in the trusted base layers
// (plugin dirs + global config) — the same non-cascading rule as
// WorkspaceProviderConfig (ADR "goal-as-task" D6: a resource's observation is
// arbitrary shell, so only user/machine-owned layers may supply it, never a
// cloned workspace dir's `.plect/`).
//
// A ResourceDef is deliberately a different, narrower concept than
// WorkspaceProviderConfig. WorkspaceProviderConfig resolves a *session's*
// resource identifier (the argument to `create`/`up`) to a session id and a
// workspace. ResourceDef instead gives a *task-instance-bound* resource id (the
// `--resource` an instance carries, independent of the session it lives in)
// an id syntax and an observation contract: which ids it recognizes (Match),
// how to read its current state (Observe), and the shape that state must
// have (StateSchema). Both may recognize overlapping id spaces (one url may
// be both a session resource identifier and an instance resource) without
// being the same declaration — they answer different questions.
type ResourceDef struct {
	ID string `toml:"-"`
	// Match is a regex a resource id must satisfy to be this kind. Required —
	// unlike WorkspaceProviderConfig's optional resolver, a resource
	// definition exists only to recognize and observe ids, so it has no
	// other reason to load.
	Match string `toml:"match"`
	// Observe runs to read the resource's current state. It must emit a JSON
	// object on stdout, validated against StateSchema when one is declared.
	// The only template variable is .ResourceID — a resource observes itself,
	// not a task instance, so it never sees session/task context.
	Observe string `toml:"observe"`
	// Finalize is optional: it runs once, after a task instance's done_when is
	// reconfirmed satisfied at the current revision, to record completion
	// against the resource (e.g. writing a frontmatter status). A definition
	// without one has no finalization step — `plect task finalize` then skips
	// straight to `plect task cleanup` (ADR D4: cleanup itself never gains
	// completion semantics).
	Finalize        string         `toml:"finalize"`
	StateSchema     map[string]any `toml:"state_schema"`
	StateSchemaFile string         `toml:"state_schema_file"`
	BaseDir         string         `toml:"-"`
	SourcePath      string         `toml:"-"`
}

// ResolvedStateSchemaPath joins StateSchemaFile with BaseDir.
func (r ResourceDef) ResolvedStateSchemaPath() string {
	return resolveSchemaPath(r.StateSchemaFile, r.BaseDir)
}

// LoadResourceDefs loads `resources/*.toml` from the trusted base layers only:
// plugin dirs first, then the global config dir; the global layer's same-id
// file replaces a plugin layer's, but two plugin layers declaring the same
// id is a load error (see loadTrustedLayer). Mirrors LoadWorkspaceProviders —
// the per-workspace-dir ancestor cascade is deliberately excluded, for the
// same reason.
func (c *Config) LoadResourceDefs() (map[string]ResourceDef, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "config", "resources"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "resources")
	}
	out, err := loadTrustedLayer(pluginDirs, globalDir, func(path string) (ResourceDef, error) {
		def, err := loadResourceDefFile(path)
		if err != nil {
			return ResourceDef{}, fmt.Errorf("resource %s: %w", path, err)
		}
		return def, nil
	}, func(def ResourceDef) string { return def.ID })
	if err != nil {
		return nil, err
	}
	var hooks []hookSource
	for _, def := range out {
		hooks = append(hooks,
			hookSource{desc: fmt.Sprintf("resource %q observe", def.ID), sourcePath: def.SourcePath, script: def.Observe},
			hookSource{desc: fmt.Sprintf("resource %q finalize", def.ID), sourcePath: def.SourcePath, script: def.Finalize},
		)
	}
	if err := checkBinRefs(hooks, c.Plugins, c.catalogRegistrations, c.catalogLock, c.catalogCacheRoot); err != nil {
		return nil, err
	}
	return out, nil
}

func loadResourceDefFile(path string) (ResourceDef, error) {
	stem, err := validateStem(path, workflowStemRE, "resource")
	if err != nil {
		return ResourceDef{}, err
	}
	var r ResourceDef
	md, err := toml.DecodeFile(path, &r)
	if err != nil {
		return r, err
	}
	for _, key := range md.Undecoded() {
		if len(key) == 1 && key[0] == "execution" {
			return r, fmt.Errorf("`execution` is retired along with the environment execution plane; see docs/migrations/")
		}
	}
	r.ID = stem
	r.SourcePath = path
	r.BaseDir = configFileDir(path)
	if r.Match == "" {
		return r, fmt.Errorf("`match` is required: a resource definition must declare which resource ids it recognizes")
	}
	if r.Observe == "" {
		return r, fmt.Errorf("`observe` is required: a resource definition must declare how to read its state")
	}
	if _, err := regexp.Compile(r.Match); err != nil {
		return r, fmt.Errorf("`match` %q does not compile: %w", r.Match, err)
	}
	return r, nil
}
