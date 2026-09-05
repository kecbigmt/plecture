package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kecbigmt/plecture/app/internal/confighome"
	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// CatalogsSchemaVersion is the only catalogs.toml schema_version this build
// understands.
const CatalogsSchemaVersion = 2

// CatalogEntry is one [[catalogs]] registration in catalogs.toml: the trust
// act itself. Registering a catalog binds a user-chosen alias to one exact
// source (never a prefix) and the plugin paths enabled from it.
type CatalogEntry struct {
	Alias   string   `toml:"alias"`
	Source  string   `toml:"source"`
	Subdir  string   `toml:"subdir,omitempty"`
	Plugins []string `toml:"plugins"`
}

// CatalogRegistrations is the parsed contents of ~/.config/plect/catalogs.toml.
type CatalogRegistrations struct {
	SchemaVersion int            `toml:"schema_version"`
	Catalogs      []CatalogEntry `toml:"catalogs"`
}

// DefaultCatalogsPath returns catalogs.toml under the resolved config home
// (~/.config/plect by default; see confighome.Resolve).
func DefaultCatalogsPath() (string, error) {
	home, err := confighome.Resolve()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "catalogs.toml"), nil
}

// LoadCatalogRegistrations reads catalogs.toml. A missing file is not an
// error: it means the user has registered no catalogs, the default state.
func LoadCatalogRegistrations(path string) (*CatalogRegistrations, error) {
	r := &CatalogRegistrations{SchemaVersion: CatalogsSchemaVersion}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return r, nil
	}
	if _, err := toml.DecodeFile(path, r); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if r.SchemaVersion != CatalogsSchemaVersion {
		return nil, fmt.Errorf("%s: schema_version %d is not supported (want %d)", path, r.SchemaVersion, CatalogsSchemaVersion)
	}
	return r, nil
}

// SaveCatalogRegistrations writes catalogs.toml atomically, creating its
// parent directory if needed.
func SaveCatalogRegistrations(path string, r *CatalogRegistrations) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = CatalogsSchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(r); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return atomicfile.Write(path, []byte(buf.String()))
}

// Find returns the registration for alias, if any.
func (r *CatalogRegistrations) Find(alias string) (CatalogEntry, bool) {
	for _, c := range r.Catalogs {
		if c.Alias == alias {
			return c, true
		}
	}
	return CatalogEntry{}, false
}

// SplitPluginID splits a catalog-qualified plugin identity
// "<catalog-alias>/<relative-path>" into its catalog registration and the
// plugin's catalog-relative path, failing if the alias names no registered
// catalog or the path is not among that catalog's enabled plugins.
func (r *CatalogRegistrations) SplitPluginID(id string) (CatalogEntry, string, error) {
	alias, path, ok := strings.Cut(id, "/")
	if !ok || alias == "" || path == "" {
		return CatalogEntry{}, "", fmt.Errorf("plugin id %q must be \"<catalog-alias>/<path>\"", id)
	}
	entry, ok := r.Find(alias)
	if !ok {
		return CatalogEntry{}, "", fmt.Errorf("plugin id %q: catalog alias %q is not registered", id, alias)
	}
	for _, p := range entry.Plugins {
		if p == path {
			return entry, path, nil
		}
	}
	return CatalogEntry{}, "", fmt.Errorf("plugin id %q: path %q is not enabled from catalog %q", id, path, alias)
}
