package config

import "fmt"

// loadTrustedLayer loads TOML files across pluginDirs then globalDir into an
// id-keyed map: loadOne parses one file, idOf extracts its id from the
// parsed value. A same-id collision between two different pluginDirs
// entries is a load error — per the plugin-packaging design, only a
// deeper, user-owned layer (globalDir) may replace what a plugin layer
// defines; declaration order between two plugin layers must never silently
// decide a conflict. globalDir == "" means there is no global layer to
// apply.
func loadTrustedLayer[T any](pluginDirs []string, globalDir string, loadOne func(path string) (T, error), idOf func(T) string) (map[string]T, error) {
	out := make(map[string]T)
	pluginOwner := make(map[string]string)
	for _, dir := range pluginDirs {
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			v, err := loadOne(path)
			if err != nil {
				return nil, err
			}
			id := idOf(v)
			if owner, exists := pluginOwner[id]; exists {
				return nil, fmt.Errorf("id %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", id, owner, path)
			}
			pluginOwner[id] = path
			out[id] = v
		}
	}
	if globalDir == "" {
		return out, nil
	}
	entries, err := listTOMLFiles(globalDir)
	if err != nil {
		return nil, err
	}
	for _, path := range entries {
		v, err := loadOne(path)
		if err != nil {
			return nil, err
		}
		out[idOf(v)] = v
	}
	return out, nil
}
