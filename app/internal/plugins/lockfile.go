package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// LockfileSchemaVersion is the current plect.lock schema version.
const LockfileSchemaVersion = 1

// CatalogLockRecord is one [[catalogs]] entry in plect.lock: the last
// explicitly trusted catalog snapshot. CatalogResolvedRevision is git-only
// (the resolved commit SHA); a locked or editable path catalog has no
// catalog-level revision, only per-plugin content hashes.
type CatalogLockRecord struct {
	Alias                   string `toml:"alias"`
	CatalogSource           string `toml:"catalog_source"`
	CatalogResolvedRevision string `toml:"catalog_resolved_revision"`
}

// PluginLockEntry is one [[plugins]] entry in plect.lock: exactly what was
// mounted for one catalog-qualified plugin identity. It carries mechanical
// pinning only — trust policy lives in catalogs.toml, never here.
type PluginLockEntry struct {
	ID                      string `toml:"id"`
	CatalogAlias            string `toml:"catalog_alias"`
	CatalogSource           string `toml:"catalog_source"`
	CatalogResolvedRevision string `toml:"catalog_resolved_revision"`
	Path                    string `toml:"path"`
	ContentHash             string `toml:"content_hash"`
	Version                 string `toml:"version"`
	PlectMinVersion         string `toml:"plect_min_version"`
	Editable                bool   `toml:"editable"`
}

// Lockfile is the parsed contents of ~/.config/plect/plect.lock.
type Lockfile struct {
	SchemaVersion int                 `toml:"schema_version"`
	Catalogs      []CatalogLockRecord `toml:"catalogs"`
	Plugins       []PluginLockEntry   `toml:"plugins"`
}

// DefaultLockfilePath returns ~/.config/plect/plect.lock.
func DefaultLockfilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "plect", "plect.lock"), nil
}

// LoadLockfile reads plect.lock. A missing file is not an error: it means
// nothing has been locked yet.
func LoadLockfile(path string) (*Lockfile, error) {
	lf := &Lockfile{SchemaVersion: LockfileSchemaVersion}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return lf, nil
	}
	if _, err := toml.DecodeFile(path, lf); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if lf.SchemaVersion != LockfileSchemaVersion {
		return nil, fmt.Errorf("%s: schema_version %d is not supported (want %d)", path, lf.SchemaVersion, LockfileSchemaVersion)
	}
	return lf, nil
}

// SaveLockfile writes plect.lock atomically, creating its parent directory
// if needed.
func SaveLockfile(path string, lf *Lockfile) error {
	if lf.SchemaVersion == 0 {
		lf.SchemaVersion = LockfileSchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(lf); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return atomicfile.Write(path, []byte(buf.String()))
}

// FindCatalog returns the lock record for alias, if any.
func (lf *Lockfile) FindCatalog(alias string) (CatalogLockRecord, bool) {
	for _, c := range lf.Catalogs {
		if c.Alias == alias {
			return c, true
		}
	}
	return CatalogLockRecord{}, false
}

// PutCatalog inserts or replaces the lock record for record.Alias.
func (lf *Lockfile) PutCatalog(record CatalogLockRecord) {
	for i, c := range lf.Catalogs {
		if c.Alias == record.Alias {
			lf.Catalogs[i] = record
			return
		}
	}
	lf.Catalogs = append(lf.Catalogs, record)
}

// FindPlugin returns the lock entry for id, if any.
func (lf *Lockfile) FindPlugin(id string) (PluginLockEntry, bool) {
	for _, p := range lf.Plugins {
		if p.ID == id {
			return p, true
		}
	}
	return PluginLockEntry{}, false
}

// PutPlugin inserts or replaces the lock entry for entry.ID.
func (lf *Lockfile) PutPlugin(entry PluginLockEntry) {
	for i, p := range lf.Plugins {
		if p.ID == entry.ID {
			lf.Plugins[i] = entry
			return
		}
	}
	lf.Plugins = append(lf.Plugins, entry)
}

// RemovePlugin deletes the lock entry for id, if any.
func (lf *Lockfile) RemovePlugin(id string) {
	for i, p := range lf.Plugins {
		if p.ID == id {
			lf.Plugins = append(lf.Plugins[:i], lf.Plugins[i+1:]...)
			return
		}
	}
}
