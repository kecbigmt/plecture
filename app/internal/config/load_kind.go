package config

import (
	"github.com/kecbigmt/plecture/app/internal/lang"
)

func loadKind[T any](c *Config, projectRoot string, kind lang.Kind, one func(*lang.Definition, bool) (T, error)) (map[string]T, error) {
	layers, err := c.discoverLayers(projectRoot)
	if err != nil {
		return nil, err
	}
	resolved, err := c.resolveNamespace(layers, kind)
	if err != nil {
		return nil, err
	}
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
