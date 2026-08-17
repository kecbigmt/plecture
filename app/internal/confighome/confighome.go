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
// this env var, which wins over the XDG default. It stays independent of
// XDGEnvVar (rather than being retired in its favor) because it is the
// right knob for a service unit pinning an exact path, unaffected by
// whatever XDG_CONFIG_HOME happens to be in that unit's environment.
const EnvVar = "PLECT_CONFIG_HOME"

// XDGEnvVar is the XDG Base Directory variable this package honors as a
// fallback between EnvVar and the hardcoded default, per the XDG Base
// Directory Specification.
const XDGEnvVar = "XDG_CONFIG_HOME"

// Resolve returns the active config home: EnvVar if set, else
// XDGEnvVar+"/plect" if set, else ~/.config/plect.
func Resolve() (string, error) {
	if v := os.Getenv(EnvVar); v != "" {
		return v, nil
	}
	if v := os.Getenv(XDGEnvVar); v != "" {
		return filepath.Join(v, "plect"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "plect"), nil
}
