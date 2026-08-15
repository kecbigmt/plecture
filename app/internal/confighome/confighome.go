// Package confighome resolves the directory plect reads declarations from.
// Runtime data and the plugin cache resolve from the XDG data/cache dirs
// independently: declarations are portable, state is machine-local, so
// nothing here touches those paths.
package confighome

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar follows the KUBECONFIG precedent: an explicit CLI flag wins over
// this env var, which wins over the XDG default.
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
