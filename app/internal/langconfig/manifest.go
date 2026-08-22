package langconfig

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// PluginManifest is a plugin.toml: the package's identity, the executables
// it owns, and the services the resident process supervises. It intentionally
// duplicates app/internal/plugins.Manifest's shape rather than importing it:
// this validator implements the closed-surface, diagnostic-typed rules the
// language now specifies, independent of the existing runtime loader (see
// the package doc comment) — the two are allowed to diverge in strictness
// until a later slice cuts the runtime surface over to this one.
type PluginManifest struct {
	SchemaVersion   int                  `toml:"schema_version"`
	Version         string               `toml:"version"`
	PlectMinVersion string               `toml:"plect_min_version"`
	Description     string               `toml:"description"`
	Executables     []ManifestExecutable `toml:"executables"`
	Services        []ManifestService    `toml:"services"`
}

// ManifestExecutable is one [[executables]] entry.
type ManifestExecutable struct {
	Name  string `toml:"name"`
	Path  string `toml:"path"`
	Build string `toml:"build"`
}

// ManifestService is one [[services]] entry.
type ManifestService struct {
	Name        string                `toml:"name"`
	Executable  string                `toml:"executable"`
	Args        []string              `toml:"args"`
	Env         map[string]string     `toml:"env"`
	RequiredEnv []string              `toml:"required_env"`
	Restart     string                `toml:"restart"`
	Health      ManifestServiceHealth `toml:"health"`
}

// ManifestServiceHealth is a service's health policy.
type ManifestServiceHealth struct {
	Type string `toml:"type"`
}

// CatalogManifestFile is a catalog.toml: which plugins the catalog publishes.
type CatalogManifestFile struct {
	SchemaVersion int      `toml:"schema_version"`
	Description   string   `toml:"description"`
	Plugins       []string `toml:"plugins"`
}

// ValidatePluginManifest reads and validates plugin.toml against the #plugin
// schema entry's structural rules (schema_version, version,
// plect_min_version, and description required; no other field) and the one
// semantic rule in scope for this slice: a service's executable must name a
// declared executable. Executable-name uniqueness and the
// plect_min_version-vs-running-binary check are not part of the 40-code
// diagnostic vocabulary and stay with the existing operational loader in
// app/internal/plugins.
func ValidatePluginManifest(path string) (*PluginManifest, error) {
	var m PluginManifest
	meta, err := toml.DecodeFile(path, &m)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"schema_version", "version", "plect_min_version", "description"} {
		if !meta.IsDefined(field) {
			return nil, newDiag(CodeFieldRequired, LayerStructural, Position{File: path, Path: field},
				fmt.Sprintf("%s is required", field))
		}
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0].String()
		return nil, newDiag(CodeFieldUnknown, LayerStructural, Position{File: path, Path: key},
			fmt.Sprintf("field %q is not part of a plugin manifest's surface", key))
	}
	declared := make(map[string]bool, len(m.Executables))
	for _, ex := range m.Executables {
		declared[ex.Name] = true
	}
	for _, svc := range m.Services {
		if !declared[svc.Executable] {
			return nil, newDiag(CodeUnknownRef, LayerSemantic,
				Position{File: path, Path: fmt.Sprintf("services.%s.executable", svc.Name)},
				fmt.Sprintf("service %q names executable %q, which this manifest does not declare", svc.Name, svc.Executable))
		}
	}
	return &m, nil
}

// ValidateCatalogManifest reads and validates catalog.toml against the
// #catalog schema entry's structural rules: schema_version and plugins are
// required, and no other field is accepted. Path-containment and
// allowlist-completeness (a plugin.toml found but not listed) are trust/
// operational rules, not part of the 40-code diagnostic vocabulary, and stay
// with the existing loader in app/internal/plugins.
func ValidateCatalogManifest(path string) (*CatalogManifestFile, error) {
	var m CatalogManifestFile
	meta, err := toml.DecodeFile(path, &m)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"schema_version", "plugins"} {
		if !meta.IsDefined(field) {
			return nil, newDiag(CodeFieldRequired, LayerStructural, Position{File: path, Path: field},
				fmt.Sprintf("%s is required", field))
		}
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0].String()
		return nil, newDiag(CodeFieldUnknown, LayerStructural, Position{File: path, Path: key},
			fmt.Sprintf("field %q is not part of a catalog manifest's surface", key))
	}
	return &m, nil
}
