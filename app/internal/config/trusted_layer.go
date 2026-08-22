package config

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// loadTrustedLayer loads TOML files across pluginDirs then globalDir into an
// id-keyed map: loadOne parses one file into the definitions it declares
// (fromPlugin says whether a plugin layer wrote it), idOf extracts each
// definition's id.
//
// Two different collisions are two different rules. Inside one layer — one
// plugin, or the global config — a repeated id is the language's
// PLECTURE-CFG-ID-DUPLICATE: a document declares its own ids, so two files
// claiming one id is ambiguous and file order must not silently pick a
// winner. Between two different pluginDirs entries it is the
// plugin-packaging rule instead: only a deeper, user-owned layer (globalDir)
// may replace what a plugin layer defines, and declaration order between two
// plugin layers must never decide a conflict. globalDir == "" means there is
// no global layer to apply.
func loadTrustedLayer[T any](pluginDirs []string, globalDir string, loadOne func(path string, fromPlugin bool) ([]T, error), idOf func(T) string) (map[string]T, error) {
	out := make(map[string]T)
	pluginOwner := make(map[string]string)
	for _, dir := range pluginDirs {
		layerOwner := make(map[string]string)
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			loaded, err := loadOne(path, true)
			if err != nil {
				return nil, err
			}
			for _, v := range loaded {
				id := idOf(v)
				if prior, dup := layerOwner[id]; dup {
					return nil, lang.DuplicateID(id, prior, path)
				}
				if owner, exists := pluginOwner[id]; exists {
					return nil, fmt.Errorf("id %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", id, owner, path)
				}
				layerOwner[id] = path
				pluginOwner[id] = path
				out[id] = v
			}
		}
	}
	if globalDir == "" {
		return out, nil
	}
	globalOwner := make(map[string]string)
	entries, err := listTOMLFiles(globalDir)
	if err != nil {
		return nil, err
	}
	for _, path := range entries {
		loaded, err := loadOne(path, false)
		if err != nil {
			return nil, err
		}
		for _, v := range loaded {
			id := idOf(v)
			if prior, dup := globalOwner[id]; dup {
				return nil, lang.DuplicateID(id, prior, path)
			}
			globalOwner[id] = path
			out[id] = v
		}
	}
	return out, nil
}
