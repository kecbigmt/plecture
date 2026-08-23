package config

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// loadTrustedKind selects one kind's declarations from the trusted base
// layers — every plugin's config root, then the machine's global config —
// and reads each with one, which turns a discovered declaration into the
// runtime's own shape.
//
// The per-workspace-dir ancestor cascade is deliberately excluded: a
// workspace provider, a resource observer and a channel decide how a
// session's identity is resolved and how a process is reached, and those
// belong to the machine rather than to any directory a project owns. An
// ancestor overlay declaring one is refused by discovery rather than ignored
// here, so an author gets told instead of wondering.
//
// Two different collisions are two different rules. Inside one layer a
// repeated id is the language's PLECTURE-CFG-ID-DUPLICATE, which discovery
// raises for the whole root. Between two plugin layers it is the
// plugin-packaging rule: only the deeper, user-owned global layer may replace
// what a plugin layer defines, and declaration order between two plugin
// layers must never decide a conflict.
func loadTrustedKind[T any](layers []discoveredLayer, kind lang.Kind, one func(*lang.Definition, bool) (T, error), idOf func(T) string) (map[string]T, error) {
	out := make(map[string]T)
	pluginOwner := make(map[string]string)
	for _, discovered := range layers {
		if discovered.layer.scope() != layerScopeTrusted {
			continue
		}
		for _, def := range discovered.ofKind(kind) {
			loaded, err := one(def, discovered.layer.plugin)
			if err != nil {
				return nil, err
			}
			id := idOf(loaded)
			if discovered.layer.plugin {
				if owner, exists := pluginOwner[id]; exists {
					return nil, fmt.Errorf("id %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", id, owner, def.File)
				}
				pluginOwner[id] = def.File
			}
			out[id] = loaded
		}
	}
	return out, nil
}

// trustedLayers reads the base layers alone. A caller loading a kind the
// cascade does not reach has no workspace directory to pass, and reading the
// overlays anyway would only discover declarations it must then refuse.
func (c *Config) trustedLayers() ([]discoveredLayer, error) {
	return c.discoverLayers("")
}
