package plugins

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ManifestSchemaVersion is the only plugin.toml schema_version this build
// understands. An unknown value fails loud rather than degrading — see
// docs/design/plugin-packaging.md's Compatibility section.
const ManifestSchemaVersion = 1

// Manifest is a plugin's plugin.toml, its only required file. It carries no
// identity field: a plugin's identity is `<catalog-alias>/<relative-path>`,
// derived entirely from the catalog that lists it (see CatalogManifest).
type Manifest struct {
	SchemaVersion   int          `toml:"schema_version"`
	Version         string       `toml:"version"`
	PlectMinVersion string       `toml:"plect_min_version"`
	Description     string       `toml:"description"`
	Executables     []Executable `toml:"executables"`
	Services        []Service    `toml:"services"`
}

// Executable is one [[executables]] entry.
type Executable struct {
	Name  string `toml:"name"`
	Path  string `toml:"path"`
	Build string `toml:"build"`
}

// Service is one [[services]] entry: a plugin-owned daemon supervised by
// `plect serve`. Its full identity is `<plugin-id>/<name>`.
type Service struct {
	Name        string            `toml:"name"`
	Executable  string            `toml:"executable"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	RequiredEnv []string          `toml:"required_env"`
	Restart     string            `toml:"restart"`
	Health      ServiceHealth     `toml:"health"`
}

// ServiceHealth is a service's health policy.
type ServiceHealth struct {
	Type string `toml:"type"`
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
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("%s: schema_version %d is not supported (want %d)", path, m.SchemaVersion, ManifestSchemaVersion)
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
	}

	svcNames := make(map[string]bool, len(m.Services))
	for i, svc := range m.Services {
		if svc.Name == "" {
			return Manifest{}, fmt.Errorf("%s: services[%d]: `name` is required", path, i)
		}
		if svcNames[svc.Name] {
			return Manifest{}, fmt.Errorf("%s: services[%d]: duplicate service name %q", path, i, svc.Name)
		}
		svcNames[svc.Name] = true
		if svc.Executable == "" {
			return Manifest{}, fmt.Errorf("%s: services[%d] %q: `executable` is required", path, i, svc.Name)
		}
		// A service can only name its own plugin's executable — see
		// docs/design/plugin-packaging.md's [[services]] field table. Cross-
		// plugin service executables would reopen the dependency edges the
		// plugin service lifecycle ADR rules out.
		if !execNames[svc.Executable] {
			return Manifest{}, fmt.Errorf("%s: services[%d] %q: executable %q is not declared by this plugin's [[executables]]", path, i, svc.Name, svc.Executable)
		}
		switch svc.Restart {
		case "":
			m.Services[i].Restart = ServiceRestartOnFailure
		case ServiceRestartOnFailure, ServiceRestartNever:
		default:
			return Manifest{}, fmt.Errorf("%s: services[%d] %q: `restart` %q is not a supported restart policy (want %q or %q)", path, i, svc.Name, svc.Restart, ServiceRestartOnFailure, ServiceRestartNever)
		}
		switch svc.Health.Type {
		case "":
			m.Services[i].Health.Type = ServiceHealthProcess
		case ServiceHealthProcess:
		default:
			return Manifest{}, fmt.Errorf("%s: services[%d] %q: health type %q is not supported (want %q)", path, i, svc.Name, svc.Health.Type, ServiceHealthProcess)
		}
	}
	return m, nil
}
