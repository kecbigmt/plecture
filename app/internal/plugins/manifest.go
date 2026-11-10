package plugins

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ManifestSchemaVersion is the only plugin.toml schema_version this build
// understands. An unknown value fails loud rather than degrading — see
// docs/design/plugin-packaging.md's Compatibility section.
const ManifestSchemaVersion = 3

// Manifest is a plugin's plugin.toml, its only required file. It carries no
// identity field: a plugin's identity is `<catalog-alias>/<relative-path>`,
// derived entirely from the catalog that lists it (see CatalogManifest).
type Manifest struct {
	SchemaVersion   int          `toml:"schema_version"`
	Version         string       `toml:"version"`
	PlectMinVersion string       `toml:"plect_min_version"`
	Description     string       `toml:"description"`
	Executables     []Executable `toml:"executables"`
}

// Executable is one [[executables]] entry.
type Executable struct {
	Name         string         `toml:"name"`
	Path         string         `toml:"path"`
	Build        string         `toml:"build"`
	Service      bool           `toml:"service"`
	ConfigSchema map[string]any `toml:"config_schema"`
}

// Service restart policies and health kinds. ServiceHealthProcess is the
// only health kind v1 supports: a running child process is healthy. Future
// health kinds must stay provider-agnostic, per the plugin service lifecycle
// ADR.
const (
	ServiceRestartOnFailure = "on-failure"
	ServiceRestartNever     = "never"

	ServiceHealthProcess = "process"
)

// LoadManifest reads and validates plugin.toml from pluginDir.
func LoadManifest(pluginDir string) (Manifest, error) {
	path := filepath.Join(pluginDir, "plugin.toml")
	var m Manifest
	meta, err := toml.DecodeFile(path, &m)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("%s: schema_version %d is not supported (want %d)", path, m.SchemaVersion, ManifestSchemaVersion)
	}
	if meta.IsDefined("services") {
		return Manifest{}, fmt.Errorf("%s: plugin [[services]] is retired; see docs/migrations/operator-declared-services.md", path)
	}
	for _, key := range meta.Undecoded() {
		if strings.HasPrefix(key.String(), "executables.config_schema.") {
			continue
		}
		return Manifest{}, fmt.Errorf("%s: unknown field %s", path, key)
	}
	if m.PlectMinVersion == "" {
		return Manifest{}, fmt.Errorf("%s: `plect_min_version` is required", path)
	}
	if _, err := parseSemver(m.PlectMinVersion); err != nil {
		return Manifest{}, fmt.Errorf("%s: `plect_min_version`: %w", path, err)
	}
	execNames := make(map[string]bool, len(m.Executables))
	for i, ex := range m.Executables {
		if ex.Name == "" {
			return Manifest{}, fmt.Errorf("%s: executables[%d]: `name` is required", path, i)
		}
		if ex.Path == "" {
			return Manifest{}, fmt.Errorf("%s: executables[%d] %q: `path` is required", path, i, ex.Name)
		}
		if execNames[ex.Name] {
			return Manifest{}, fmt.Errorf("%s: executables[%d]: duplicate executable name %q", path, i, ex.Name)
		}
		execNames[ex.Name] = true
		if ex.Service && ex.ConfigSchema == nil {
			return Manifest{}, fmt.Errorf("%s: executables[%d] %q: service executable requires config_schema", path, i, ex.Name)
		}
	}

	return m, nil
}
