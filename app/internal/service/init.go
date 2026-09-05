package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// InitConfigValues are the root config.toml fields `plect init` writes on
// the user's behalf: the small set of genuinely per-user values, not
// anything init defaults on its own — every value here came from an
// explicit answer.
type InitConfigValues struct {
	WorkspaceDirsRoot string
	ResourceAllowlist []string
}

// initConfigDoc mirrors config.Config's toml tags for exactly the fields
// init writes. Encoding config.Config directly would also serialize every
// other field's Go zero value (e.g. detached = false) as an explicit
// override of config.DefaultConfig's baseline, which init never asked
// about.
type initConfigDoc struct {
	SchemaVersion     int      `toml:"schema_version"`
	WorkspaceDirsRoot string   `toml:"workspace_dirs_root"`
	ResourceAllowlist []string `toml:"resource_allowlist,omitempty"`
}

// WriteInitConfig creates a fresh config.toml at path. It refuses to
// overwrite an existing file — the caller (InitAlreadyDone) is responsible
// for having confirmed this config home was empty before commiting to the
// rest of the init flow, and this is the last-line backstop for that
// invariant.
func WriteInitConfig(path string, values InitConfigValues) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("create %s: %v", path, err)}
	}
	defer f.Close()

	doc := initConfigDoc{SchemaVersion: lang.KnownSchemaVersion, WorkspaceDirsRoot: values.WorkspaceDirsRoot, ResourceAllowlist: values.ResourceAllowlist}
	if err := toml.NewEncoder(f).Encode(doc); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return nil
}

// InitAlreadyDone reports whether this config home has already been
// bootstrapped: either config.toml exists, or a catalog is already
// registered. Either signal on its own means `plect init` must refuse
// rather than clobber a hand-authored config.toml or silently register a
// second catalog on top of one added some other way.
func InitAlreadyDone(configPath string, paths PluginPaths) (bool, error) {
	if _, err := os.Stat(configPath); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return len(registrations.Catalogs) > 0, nil
}
