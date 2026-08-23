// Package providervocab derives the provider-name vocabulary
// scripts/check-provider-boundary.sh treats as leaked into core: a shipped
// plugin's directory id, plus every executable name its plugin.toml
// declares. It decodes plugin.toml through the same manifest loader plect
// itself uses, rather than scanning it as text, so a commented-out
// executable or a name embedded in some other field can't be mistaken for
// live vocabulary.
package providervocab

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// Collect returns the sorted, de-duplicated provider vocabulary declared
// under pluginsRoot: each subdirectory with a plugin.toml contributes its
// directory name (the plugin id) and every name its `[[executables]]`
// declares. A subdirectory without a plugin.toml is not a plugin and is
// skipped.
func Collect(pluginsRoot string) ([]string, error) {
	entries, err := os.ReadDir(pluginsRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pluginsRoot, err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(pluginsRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err != nil {
			continue
		}
		manifest, err := plugins.LoadManifest(dir)
		if err != nil {
			return nil, err
		}
		seen[entry.Name()] = true
		for _, ex := range manifest.Executables {
			seen[ex.Name] = true
		}
	}
	words := make([]string, 0, len(seen))
	for w := range seen {
		words = append(words, w)
	}
	sort.Strings(words)
	return words, nil
}
