// Package providervocab derives the provider-name vocabulary
// scripts/check-provider-boundary.sh treats as leaked into core: a shipped
// plugin's id, plus every executable name its plugin.toml declares. It
// decodes plugin.toml and catalog.toml through the same manifest loaders
// plect itself uses, rather than scanning them as text, so a commented-out
// executable or a name embedded in some other field can't be mistaken for
// live vocabulary.
package providervocab

import (
	"path/filepath"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// Collect returns the sorted, de-duplicated provider vocabulary published
// by the catalog rooted at pluginsRoot: each listed plugin's id (the last
// path segment of its catalog.toml entry) and every name its
// `[[executables]]` declares. Publication is read from catalog.toml's
// `plugins` list — the same explicit, reviewable enumeration
// LoadCatalogManifest already treats as authoritative — rather than by
// scanning pluginsRoot's subdirectories: a plugin.toml present but not yet
// listed is not a published plugin, and LoadCatalogManifest already fails
// the reverse case (a listed plugin with no plugin.toml, or a plugin.toml
// nothing lists) before this ever runs.
func Collect(pluginsRoot string) ([]string, error) {
	catalog, err := plugins.LoadCatalogManifest(pluginsRoot)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range catalog.Plugins {
		seen[filepath.Base(filepath.Clean(p))] = true
		manifest, err := plugins.LoadManifest(filepath.Join(pluginsRoot, p))
		if err != nil {
			return nil, err
		}
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
