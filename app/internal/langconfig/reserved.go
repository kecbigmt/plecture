package langconfig

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// KnownSchemaVersion is the only config-language dialect this build
// understands. It governs config.toml, catalogs.toml, and plect.lock alike:
// docs/language/config.md's "Reserved root files" validation rules apply the
// same directional schema_version comparison to all three.
const KnownSchemaVersion = 1

// ReservedFileNames are the three reserved root files in the user config
// home. A trusted definition root's recursive discovery sweep skips them —
// they carry machine-wide settings and resolution state, never definitions.
var ReservedFileNames = map[string]bool{
	"config.toml":   true,
	"catalogs.toml": true,
	"plect.lock":    true,
}

// ConfigToml is the reserved config.toml: machine-wide settings governing
// the whole user config tree.
type ConfigToml struct {
	SchemaVersion     int            `toml:"schema_version"`
	WorkspaceDirsRoot string         `toml:"workspace_dirs_root"`
	ResourceAllowlist []string       `toml:"resource_allowlist"`
	PluginDirs        []string       `toml:"plugin_dirs"`
	Channels          []string       `toml:"channels"`
	InputsSchema      map[string]any `toml:"inputs_schema"`
	InputsSchemaFile  string         `toml:"inputs_schema_file"`
}

// CatalogsToml is the reserved catalogs.toml: the catalog aliases registered
// on this machine and the plugins enabled under each.
type CatalogsToml struct {
	SchemaVersion int                   `toml:"schema_version"`
	Catalogs      []CatalogRegistration `toml:"catalogs"`
}

// CatalogRegistration is one [[catalogs]] entry in catalogs.toml.
type CatalogRegistration struct {
	Alias   string   `toml:"alias"`
	Source  string   `toml:"source"`
	Subdir  string   `toml:"subdir"`
	Plugins []string `toml:"plugins"`
}

// LockToml is the reserved plect.lock: the resolved revisions and content
// hashes mounting is verified against.
type LockToml struct {
	SchemaVersion int           `toml:"schema_version"`
	Catalogs      []LockCatalog `toml:"catalogs"`
	Plugins       []LockPlugin  `toml:"plugins"`
}

// LockCatalog is one [[catalogs]] entry in plect.lock.
type LockCatalog struct {
	Alias                   string `toml:"alias"`
	CatalogSource           string `toml:"catalog_source"`
	Subdir                  string `toml:"subdir"`
	CatalogResolvedRevision string `toml:"catalog_resolved_revision"`
}

// LockPlugin is one [[plugins]] entry in plect.lock.
type LockPlugin struct {
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

// LoadConfigToml reads and validates config.toml against the language's
// reserved-file rules: schema_version required, unknown fields rejected
// (which is also what rejects a definition table — config.toml declares no
// arbitrary tables), and the dialect directional check.
func LoadConfigToml(path string) (*ConfigToml, error) {
	var c ConfigToml
	meta, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, err
	}
	if err := validateReservedFile(path, meta, c.SchemaVersion); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadCatalogsToml reads and validates catalogs.toml.
func LoadCatalogsToml(path string) (*CatalogsToml, error) {
	var c CatalogsToml
	meta, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, err
	}
	if err := validateReservedFile(path, meta, c.SchemaVersion); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadLockToml reads and validates plect.lock.
func LoadLockToml(path string) (*LockToml, error) {
	var l LockToml
	meta, err := toml.DecodeFile(path, &l)
	if err != nil {
		return nil, err
	}
	if err := validateReservedFile(path, meta, l.SchemaVersion); err != nil {
		return nil, err
	}
	return &l, nil
}

// validateReservedFile applies the reserved-root-file rules common to
// config.toml, catalogs.toml, and plect.lock: schema_version is required,
// no other field may go undecoded (a closed surface — which is also what
// makes a definition table in one of these files a load error), and a
// dialect other than KnownSchemaVersion is a directional error.
func validateReservedFile(path string, meta toml.MetaData, schemaVersion int) error {
	if !meta.IsDefined("schema_version") {
		return newDiag(CodeFieldRequired, LayerStructural, Position{File: path, Path: "schema_version"},
			"schema_version is required")
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0].String()
		return newDiag(CodeFieldUnknown, LayerStructural, Position{File: path, Path: key},
			fmt.Sprintf("field %q is not part of this file's surface", key))
	}
	switch {
	case schemaVersion < KnownSchemaVersion:
		return newDiag(CodeSchemaVersionOlder, LayerSemantic, Position{File: path, Path: "schema_version"},
			fmt.Sprintf("schema_version %d is a superseded dialect (this build knows %d); see docs/migrations/", schemaVersion, KnownSchemaVersion))
	case schemaVersion > KnownSchemaVersion:
		return newDiag(CodeSchemaVersionNewer, LayerSemantic, Position{File: path, Path: "schema_version"},
			fmt.Sprintf("schema_version %d is newer than this build knows (%d); upgrade plect", schemaVersion, KnownSchemaVersion))
	}
	return nil
}
