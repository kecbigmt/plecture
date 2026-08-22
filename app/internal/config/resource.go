package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// ResourceDef is one `kind = "resource_observer"` declaration from the
// trusted base layers (plugin dirs + global config) — the same non-cascading
// rule as WorkspaceProviderConfig (ADR "goal-as-task" D6: an observation
// runs whatever executable it names, so only user/machine-owned layers may
// supply it, never a cloned workspace dir's `.plect/`).
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
	ID string
	// Match is a regex a resource id must satisfy to be this kind. Required —
	// unlike WorkspaceProviderConfig's optional resolver, an observer exists
	// only to recognize and observe ids, so it has no other reason to load.
	Match string
	// Observe reads the resource's current state. It must emit a JSON object
	// on stdout, validated against StateSchema when one is declared.
	Observe *lang.Action
	// Finalize is optional: it runs once, after a task instance's done_when is
	// reconfirmed satisfied at the current revision, to record completion
	// against the resource (e.g. writing a frontmatter status). A definition
	// without one has no finalization step — `plect task finalize` then skips
	// straight to `plect task cleanup` (ADR D4: cleanup itself never gains
	// completion semantics).
	Finalize        *lang.Action
	StateSchema     map[string]any
	StateSchemaFile string
	BaseDir         string
	SourcePath      string
	// FromPlugin says a plugin layer wrote this definition, which is what
	// decides whether its bin references may name another plugin.
	FromPlugin bool
}

// ResolvedStateSchemaPath joins StateSchemaFile with BaseDir.
func (r ResourceDef) ResolvedStateSchemaPath() string {
	return resolveSchemaPath(r.StateSchemaFile, r.BaseDir)
}

// Ownership names the layer that wrote this definition, for the reference
// rules that differ between shipped and user-authored config.
func (r ResourceDef) Ownership() lang.Ownership {
	return lang.Ownership{IsPlugin: r.FromPlugin}
}

// LoadResourceDefs loads the resource observers declared under `resources/`
// in the trusted base layers only: plugin dirs first, then the global config
// dir; the global layer's same-id definition replaces a plugin layer's, but
// two plugin layers declaring the same id is a load error (see
// loadTrustedLayer). Mirrors LoadWorkspaceProviders — the per-workspace-dir
// ancestor cascade is deliberately excluded, for the same reason.
//
// Discovery is still directory-scoped rather than the language's own
// recursive sweep of a definition root, because the other kinds under a
// plugin's config/ are not on the ratified surface yet and a whole-root
// sweep would have to parse them too. It is retired when the last surface
// moves over.
func (c *Config) LoadResourceDefs() (map[string]ResourceDef, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "config", "resources"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "resources")
	}
	return loadTrustedLayer(pluginDirs, globalDir, c.loadResourceObservers, func(def ResourceDef) string { return def.ID })
}

func (c *Config) loadResourceObservers(path string, fromPlugin bool) ([]ResourceDef, error) {
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
	out := make([]ResourceDef, 0, len(parsed))
	for _, def := range parsed {
		if def.Kind != lang.KindResourceObserver {
			return nil, fmt.Errorf("%s: %q declares kind %q; a definition under resources/ is a resource_observer", path, def.ID, def.Kind)
		}
		if err := validation.ValidateDefinition(def); err != nil {
			return nil, err
		}
		observer, err := resourceDefFrom(def, path, fromPlugin)
		if err != nil {
			return nil, fmt.Errorf("resource observer %s in %s: %w", def.ID, path, err)
		}
		out = append(out, observer)
	}
	return out, nil
}

// resourceDefFrom reads the fields the runtime needs off a validated
// declaration. `match` and `observe` are required here rather than by the
// language validator, which reports only what the ratified structural schema
// also rejects: an observer that recognizes nothing or reads nothing has no
// runtime meaning, and this is the layer that would have to invent one.
func resourceDefFrom(def *lang.Definition, path string, fromPlugin bool) (ResourceDef, error) {
	r := ResourceDef{
		ID:         def.ID,
		BaseDir:    configFileDir(path),
		SourcePath: path,
		FromPlugin: fromPlugin,
	}
	if raw, ok := def.Body["match"]; ok {
		match, ok := raw.(string)
		if !ok {
			return r, fmt.Errorf("`match` is a regular expression string")
		}
		r.Match = match
	}
	if r.Match == "" {
		return r, fmt.Errorf("`match` is required: an observer must declare which resource ids it recognizes")
	}
	if _, err := regexp.Compile(r.Match); err != nil {
		return r, fmt.Errorf("`match` %q does not compile: %w", r.Match, err)
	}
	observe, err := actionField(def, path, "observe")
	if err != nil {
		return r, err
	}
	if observe == nil {
		return r, fmt.Errorf("`observe` is required: an observer must declare how to read its state")
	}
	r.Observe = observe
	if r.Finalize, err = actionField(def, path, "finalize"); err != nil {
		return r, err
	}
	if raw, ok := def.Body["state_schema"]; ok {
		schema, ok := raw.(map[string]any)
		if !ok {
			return r, fmt.Errorf("`state_schema` is a JSON Schema document")
		}
		r.StateSchema = schema
	}
	if raw, ok := def.Body["state_schema_file"]; ok {
		file, ok := raw.(string)
		if !ok {
			return r, fmt.Errorf("`state_schema_file` is a path")
		}
		r.StateSchemaFile = file
	}
	return r, nil
}

// actionField re-parses one already-validated action field into the form the
// runtime executes. ValidateDefinition parses each action to check it and
// keeps nothing, so the parse happens twice rather than the validator
// growing a second, result-returning entry point for one caller.
func actionField(def *lang.Definition, path, field string) (*lang.Action, error) {
	raw, ok := def.Body[field]
	if !ok {
		return nil, nil
	}
	return lang.ParseAction(raw, lang.Position{File: path, Path: def.ID + "." + field})
}
