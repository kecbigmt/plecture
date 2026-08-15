// Package confighome resolves the single directory plect reads declarations
// from: config.toml, catalogs.toml, plect.lock, and the global templates/,
// tasks/, workflows/, providers/, resources/, environments/, and channels/
// overlays. Runtime data (state, event logs) and the catalog cache resolve
// from the XDG data/cache dirs independently — declarations are portable,
// state is machine-local — so nothing here touches those paths.
package confighome

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar is the environment variable that overrides the config home
// directory, following the KUBECONFIG precedent: an explicit CLI flag wins
// over this, which wins over the XDG default. The root command sets this
// process env var from --config-home before any subcommand runs, so every
// resolver in this codebase can stay a plain env lookup instead of each
// needing its own flag-vs-env precedence logic.
const EnvVar = "PLECT_CONFIG_HOME"

// Resolve returns the active config home: EnvVar if set, else
// ~/.config/plect.
func Resolve() (string, error) {
	if v := os.Getenv(EnvVar); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "plect"), nil
}
