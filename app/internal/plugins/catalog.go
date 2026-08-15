package plugins

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// CatalogSchemaVersion is the only catalog.toml schema_version this build
// understands. An unknown value fails loud rather than degrading.
const CatalogSchemaVersion = 1

// CatalogManifest is a catalog.toml: hand-authored metadata whose directory
// bounds a catalog's trust space and whose `plugins` list is the exact,
// reviewable set of plugins it publishes.
type CatalogManifest struct {
	SchemaVersion int      `toml:"schema_version"`
	Plugins       []string `toml:"plugins"`
	Description   string   `toml:"description"`
}

// LoadCatalogManifest reads and validates catalog.toml from catalogRoot:
// schema_version must be supported, every listed plugin path must resolve
// (after symlinks) inside catalogRoot and contain a plugin.toml, and no
// plugin.toml elsewhere under catalogRoot may go unlisted — the strict
// allowlist a reviewer audits the published plugin set from.
func LoadCatalogManifest(catalogRoot string) (CatalogManifest, error) {
	path := filepath.Join(catalogRoot, "catalog.toml")
	var m CatalogManifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return CatalogManifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	if m.SchemaVersion != CatalogSchemaVersion {
		return CatalogManifest{}, fmt.Errorf("%s: schema_version %d is not supported (want %d)", path, m.SchemaVersion, CatalogSchemaVersion)
	}
	if len(m.Plugins) == 0 {
		return CatalogManifest{}, fmt.Errorf("%s: `plugins` declares no plugin paths", path)
	}
	if err := validateCatalogPaths(catalogRoot, m); err != nil {
		return CatalogManifest{}, err
	}
	return m, nil
}

// validateCatalogPaths enforces catalog.toml's containment and
// allowlist-completeness invariants.
func validateCatalogPaths(catalogRoot string, m CatalogManifest) error {
	realRoot, err := filepath.EvalSymlinks(catalogRoot)
	if err != nil {
		return fmt.Errorf("resolve catalog root %s: %w", catalogRoot, err)
	}

	declared := make(map[string]bool, len(m.Plugins))
	for _, p := range m.Plugins {
		clean := filepath.Clean(p)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return fmt.Errorf("catalog.toml: plugin path %q is not a relative subpath of the catalog", p)
		}
		full := filepath.Join(catalogRoot, clean)
		realFull, err := filepath.EvalSymlinks(full)
		if err != nil {
			return fmt.Errorf("catalog.toml: plugin path %q: %w", p, err)
		}
		rel, err := filepath.Rel(realRoot, realFull)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("catalog.toml: plugin path %q escapes the catalog root", p)
		}
		if _, err := os.Stat(filepath.Join(realFull, "plugin.toml")); err != nil {
			return fmt.Errorf("catalog.toml: plugin path %q has no plugin.toml", p)
		}
		declared[clean] = true
	}

	var unlisted []string
	if err := filepath.WalkDir(realRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "plugin.toml" {
			return nil
		}
		rel, relErr := filepath.Rel(realRoot, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		rel = filepath.Clean(rel)
		if !declared[rel] {
			unlisted = append(unlisted, rel)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk catalog root %s: %w", catalogRoot, err)
	}
	if len(unlisted) > 0 {
		return fmt.Errorf("catalog.toml: plugin.toml found at %v but not listed in `plugins`", unlisted)
	}
	return nil
}
