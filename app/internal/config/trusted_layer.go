package config

import (
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// loadTrustedKind selects one kind from the trusted base layers' resolved
// namespace — every plugin's config root, then the machine's global config —
// and reads each declaration with one, which turns it into the runtime's own
// shape.
func loadTrustedKind[T any](resolved []resolvedDefinition, one func(*lang.Definition, bool) (T, error)) (map[string]T, error) {
	out := make(map[string]T)
	for _, entry := range resolved {
		loaded, err := one(entry.def, entry.fromPlugin)
		if err != nil {
			return nil, err
		}
		out[entry.address] = loaded
	}
	return out, nil
}

// trustedKind resolves one kind over the plugin and global layers alone,
// because a project root cannot declare the kinds it loads.
func (c *Config) trustedKind(kind lang.Kind) ([]resolvedDefinition, error) {
	layers, err := c.discoverLayers("")
	if err != nil {
		return nil, err
	}
	return c.resolveNamespace(layers, kind)
}
