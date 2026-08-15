package config

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// EnvironmentConfig is loaded from `environments/<id>.toml` in the trusted
// base layers (plugin dirs + global config), mirroring ProviderConfig. It
// declares how a workflow's task executor runs: Setup/Cleanup acquire and
// release whatever the environment needs (a container, a remote host, ...),
// and Exec is the host-run script that launches an argv inside it.
//
// A workflow that never sets `environment` (host degeneration) never
// consults this loader at all.
//
// Environments deliberately do NOT participate in the per-workdir cascade,
// for the same reason providers don't: they must be resolvable independent
// of any working directory, and Setup/Exec/Cleanup are arbitrary shell, so
// only user/machine-owned layers may supply them.
type EnvironmentConfig struct {
	ID string `toml:"-"`
	// Setup acquires whatever the environment needs (e.g. starts a
	// container) and may emit a JSON object on stdout describing it.
	// Optional: an environment with no acquisition step (e.g. one that just
	// runs Exec against something already running) may leave this empty.
	Setup string `toml:"setup"`
	// Exec is the host-run script that launches its trailing argv inside
	// the environment (e.g. `docker exec -i -w "$PLECT_ENV_WORKDIR" ... "$@"`).
	// Required — running argv inside the environment is its reason to exist.
	Exec string `toml:"exec"`
	// Cleanup releases whatever Setup acquired. Optional, mirroring
	// ProviderConfig.Cleanup.
	Cleanup           string         `toml:"cleanup"`
	OutputsSchema     map[string]any `toml:"outputs_schema"`
	OutputsSchemaFile string         `toml:"outputs_schema_file"`
	BaseDir           string         `toml:"-"`
	SourcePath        string         `toml:"-"`
}

// ResolvedOutputsSchemaPath joins OutputsSchemaFile with BaseDir.
func (e EnvironmentConfig) ResolvedOutputsSchemaPath() string {
	return resolveSchemaPath(e.OutputsSchemaFile, e.BaseDir)
}

// LoadEnvironments loads `environments/*.toml` from the trusted base layers
// only: plugin dirs first, then the global config dir; the global layer's
// same-id file replaces a plugin layer's, but two plugin layers declaring
// the same id is a load error (see loadTrustedLayer). The per-workdir
// ancestor cascade is deliberately excluded (see EnvironmentConfig).
func (c *Config) LoadEnvironments() (map[string]EnvironmentConfig, error) {
	var pluginDirs []string
	for _, plugin := range c.PluginDirs {
		pluginDirs = append(pluginDirs, filepath.Join(plugin, "environments"))
	}
	globalDir := ""
	if c.BaseDir != "" {
		globalDir = filepath.Join(c.BaseDir, "environments")
	}
	return loadTrustedLayer(pluginDirs, globalDir, func(path string) (EnvironmentConfig, error) {
		e, err := loadEnvironmentFile(path)
		if err != nil {
			return EnvironmentConfig{}, fmt.Errorf("environment %s: %w", path, err)
		}
		return e, nil
	}, func(e EnvironmentConfig) string { return e.ID })
}

func loadEnvironmentFile(path string) (EnvironmentConfig, error) {
	stem, err := validateStem(path, workflowStemRE, "environment")
	if err != nil {
		return EnvironmentConfig{}, err
	}
	var e EnvironmentConfig
	if _, err := toml.DecodeFile(path, &e); err != nil {
		return e, err
	}
	e.ID = stem
	e.SourcePath = path
	e.BaseDir = configFileDir(path)
	if e.Exec == "" {
		return e, fmt.Errorf("`exec` is required: an environment's purpose is running argv inside it")
	}
	return e, nil
}
