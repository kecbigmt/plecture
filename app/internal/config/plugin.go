package config

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/version"
)

// resolveDeclaredPlugins prepends cfg.PluginDirs with the mount directories
// of every plugin enabled across registered catalogs in
// ~/.config/plect/catalogs.toml, resolved purely from local state
// (plugins.VerifyAndMountAll never fetches over the network, so this runs
// on every config load). A missing catalogs.toml is not an error — it means
// the user has registered no catalogs. Any resolution failure (an
// unregistered catalog, a missing or mismatched lock entry, tampered
// content, or a plect_min_version violation) fails the whole load: per the
// plugin-packaging design, a plugin problem must never silently mount
// nothing and continue.
func resolveDeclaredPlugins(cfg *Config) (*Config, error) {
	catalogsPath, err := plugins.DefaultCatalogsPath()
	if err != nil {
		return cfg, nil
	}
	registrations, err := plugins.LoadCatalogRegistrations(catalogsPath)
	if err != nil {
		return nil, fmt.Errorf("load catalog registrations: %w", err)
	}
	if len(registrations.Catalogs) == 0 {
		return cfg, nil
	}

	lockPath, err := plugins.DefaultLockfilePath()
	if err != nil {
		return nil, fmt.Errorf("resolve plect.lock path: %w", err)
	}
	lock, err := plugins.LoadLockfile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("load plect.lock: %w", err)
	}
	cacheRoot, err := plugins.DefaultCacheRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve catalog cache root: %w", err)
	}

	mounted, err := plugins.VerifyAndMountAll(registrations, lock, cacheRoot, version.Current)
	if err != nil {
		return nil, err
	}
	resolvedDirs := make([]string, len(mounted))
	for i, m := range mounted {
		resolvedDirs[i] = m.Dir
	}
	cfg.PluginDirs = append(resolvedDirs, cfg.PluginDirs...)
	cfg.Plugins = mounted
	cfg.catalogRegistrations = registrations
	cfg.catalogLock = lock
	cfg.catalogCacheRoot = cacheRoot
	return cfg, nil
}
