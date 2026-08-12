package config

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// ResourceDef is loaded from `resources/<id>.toml` in the trusted base layers
// (plugin dirs + global config) — the same non-cascading rule as
// ProviderConfig (ADR "goal-as-task" D6: a resource's observation is
// arbitrary shell, so only user/machine-owned layers may supply it, never a
// cloned workdir's `.plect/`).
//
// A ResourceDef is deliberately a different, narrower concept than
// ProviderConfig. ProviderConfig resolves a *session's* resource identifier
// (the argument to `create`/`up`) to a session id and a working directory.
// ResourceDef instead gives a *task-instance-bound* resource id (the
// `--resource` an instance carries, independent of the session it lives in)
// an id syntax and an observation contract: which ids it recognizes (Match),
// how to read its current state (Observe), and the shape that state must
// have (StateSchema). Both may recognize overlapping id spaces (one url may
// be both a session resource identifier and an instance resource) without
// being the same declaration — they answer different questions.
type ResourceDef struct {
	ID string `toml:"-"`
	// Match is a regex a resource id must satisfy to be this kind. Required —
	// unlike ProviderConfig's optional resolver, a resource definition exists
	// only to recognize and observe ids, so it has no other reason to load.
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
	// Execution names the desired execution plane ("host" default, or
	// "environment") but is not yet consulted: resource observe/finalize
	// always run on host, since they may be called standalone (`plect resource
	// status`) with no session — and so no environment — in scope. Parsed and
	// validated so config authors can declare it ahead of that wiring.
	Execution  string `toml:"execution"`
	BaseDir    string `toml:"-"`
	SourcePath string `toml:"-"`
}

// ResolvedStateSchemaPath joins StateSchemaFile with BaseDir.
func (r ResourceDef) ResolvedStateSchemaPath() string {
	return resolveSchemaPath(r.StateSchemaFile, r.BaseDir)
}

// LoadResourceDefs loads `resources/*.toml` from the trusted base layers only:
// plugin dirs first, then the global config dir; a deeper layer's same-id
// file replaces the shallower one. Mirrors LoadProviders — the per-worktree
// ancestor cascade is deliberately excluded, for the same reason.
func (c *Config) LoadResourceDefs() (map[string]ResourceDef, error) {
	out := make(map[string]ResourceDef)
	var dirs []string
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, filepath.Join(plugin, "resources"))
	}
	if c.BaseDir != "" {
		dirs = append(dirs, filepath.Join(c.BaseDir, "resources"))
	}
	for _, dir := range dirs {
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			def, err := loadResourceDefFile(path)
			if err != nil {
				return nil, fmt.Errorf("resource %s: %w", path, err)
			}
			out[def.ID] = def
		}
	}
	return out, nil
}

func loadResourceDefFile(path string) (ResourceDef, error) {
	stem, err := validateStem(path, workflowStemRE, "resource")
	if err != nil {
		return ResourceDef{}, err
	}
	var r ResourceDef
	if _, err := toml.DecodeFile(path, &r); err != nil {
		return r, err
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
	if r.Execution != "" && r.Execution != ExecutionHost && r.Execution != ExecutionEnvironment {
		return r, fmt.Errorf("`execution` must be %q or %q, got %q", ExecutionHost, ExecutionEnvironment, r.Execution)
	}
	return r, nil
}
