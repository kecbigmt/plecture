package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/procexec"
	"github.com/kecbigmt/plecture/app/internal/version"
)

// PluginAddResult reports what PluginAdd persisted.
type PluginAddResult struct {
	ID               string
	ResolvedRevision string
	ContentHash      string
}

// PluginAdd enables one plugin path from an already-registered catalog: it
// resolves the catalog (reusing its lock record when one exists, so adding
// a second plugin from an already-added catalog needs no new fetch), runs
// the plugin's declared build commands, writes its lock entry, and appends
// path to that catalog entry's `plugins` list in catalogs.toml. Unlike
// `catalog add`, this never prompts — trust was already established when
// the catalog itself was registered.
func PluginAdd(ctx context.Context, paths PluginPaths, id string) (*PluginAddResult, error) {
	alias, path, err := splitPluginRef(id)
	if err != nil {
		return nil, err
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	entry, ok := registrations.Find(alias)
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("catalog alias %q is not registered; run `plect catalog add` first", alias)}
	}
	if stringSliceContains(entry.Plugins, path) {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("plugin %q is already enabled; use `plect plugin update`", id)}
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	fetched, err := resolveCatalogForEnable(ctx, paths, entry, lock)
	if err != nil {
		return nil, err
	}
	if !stringSliceContains(fetched.Manifest.Plugins, path) {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("catalog %q does not publish plugin path %q", alias, path)}
	}

	locked, err := lockPluginAtPath(ctx, fetched, entry, alias, path)
	if err != nil {
		return nil, err
	}
	lock.PutCatalog(plugins.CatalogLockRecord{Alias: alias, CatalogSource: entry.Source, Dir: entry.Dir, CatalogResolvedRevision: fetched.ResolvedRevision})
	lock.PutPlugin(locked)

	for i := range registrations.Catalogs {
		if registrations.Catalogs[i].Alias == alias {
			registrations.Catalogs[i].Plugins = append(registrations.Catalogs[i].Plugins, path)
		}
	}

	if err := plugins.SaveCatalogRegistrations(paths.CatalogsPath, registrations); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if err := plugins.SaveLockfile(paths.LockfilePath, lock); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return &PluginAddResult{ID: locked.ID, ResolvedRevision: fetched.ResolvedRevision, ContentHash: locked.ContentHash}, nil
}

// resolveCatalogForEnable resolves entry's content for `plugin add`: reuse
// the catalog's existing lock record when one exists (no new fetch — the
// same snapshot every other plugin from this catalog is pinned to), and
// only fetch when the catalog was never added.
func resolveCatalogForEnable(ctx context.Context, paths PluginPaths, entry plugins.CatalogEntry, lock *plugins.Lockfile) (*plugins.FetchedCatalog, error) {
	scheme, _, err := plugins.ParseSource(entry.Source)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if record, ok := lock.FindCatalog(entry.Alias); ok && scheme.IsGit() {
		root, err := plugins.ResolveCatalogDir(plugins.CacheDir(paths.CacheRoot, entry.Source, record.CatalogResolvedRevision), entry.Dir)
		if err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		manifest, err := plugins.LoadCatalogManifest(root)
		if err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		return &plugins.FetchedCatalog{Root: root, ResolvedRevision: record.CatalogResolvedRevision, Manifest: manifest}, nil
	}
	fetched, err := plugins.FetchCatalog(ctx, procexec.Default, entry.Source, "", entry.Dir, paths.CacheRoot)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return &fetched, nil
}

// PluginUpdateResult reports what PluginUpdate persisted.
type PluginUpdateResult struct {
	ID               string
	ResolvedRevision string
	ContentHash      string
}

// PluginUpdate fetches the newest matching catalog snapshot (git: an
// explicit --revision is required, mirroring `catalog update`'s no-implicit-
// tracking rule; locked path: re-resolved directly) and repoints only this
// plugin's lock entry — sibling plugins enabled from the same catalog keep
// their previously locked coordinates.
func PluginUpdate(ctx context.Context, paths PluginPaths, id string, revision string) (*PluginUpdateResult, error) {
	alias, path, err := splitPluginRef(id)
	if err != nil {
		return nil, err
	}
	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	entry, ok := registrations.Find(alias)
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("catalog alias %q is not registered", alias)}
	}
	if !stringSliceContains(entry.Plugins, path) {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("plugin %q is not enabled; run `plect plugin add` first", id)}
	}

	scheme, _, err := plugins.ParseSource(entry.Source)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	switch {
	case scheme.IsGit() && revision == "":
		return nil, &Error{Code: ErrInvalidInput, Message: "`--revision` is required to update a plugin from a git-sourced catalog"}
	case !scheme.IsGit() && revision != "":
		return nil, &Error{Code: ErrInvalidInput, Message: "`--revision` does not apply to a path-sourced catalog"}
	}

	fetched, err := plugins.FetchCatalog(ctx, procexec.Default, entry.Source, revision, entry.Dir, paths.CacheRoot)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if !stringSliceContains(fetched.Manifest.Plugins, path) {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("catalog %q: plugin %q is no longer published by the new snapshot", alias, path)}
	}

	locked, err := lockPluginAtPath(ctx, &fetched, entry, alias, path)
	if err != nil {
		return nil, err
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	// Bump the shared catalog lock record too, so a later `plugin add` for a
	// new path from this catalog reuses this fresher snapshot.
	lock.PutCatalog(plugins.CatalogLockRecord{Alias: alias, CatalogSource: entry.Source, Dir: entry.Dir, CatalogResolvedRevision: fetched.ResolvedRevision})
	lock.PutPlugin(locked)
	if err := plugins.SaveLockfile(paths.LockfilePath, lock); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	return &PluginUpdateResult{ID: locked.ID, ResolvedRevision: fetched.ResolvedRevision, ContentHash: locked.ContentHash}, nil
}

// PluginRemoveResult reports what PluginRemove disabled.
type PluginRemoveResult struct {
	ID string
}

// PluginRemove disables a single plugin: removes its path from the owning
// catalog entry's `plugins` list and deletes its lock entry.
func PluginRemove(paths PluginPaths, id string) (*PluginRemoveResult, error) {
	alias, path, err := splitPluginRef(id)
	if err != nil {
		return nil, err
	}
	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	found := false
	for i := range registrations.Catalogs {
		if registrations.Catalogs[i].Alias != alias {
			continue
		}
		kept := make([]string, 0, len(registrations.Catalogs[i].Plugins))
		for _, p := range registrations.Catalogs[i].Plugins {
			if p == path {
				found = true
				continue
			}
			kept = append(kept, p)
		}
		registrations.Catalogs[i].Plugins = kept
	}
	if !found {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("plugin %q is not enabled", id)}
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	lock.RemovePlugin(id)

	if err := plugins.SaveCatalogRegistrations(paths.CatalogsPath, registrations); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if err := plugins.SaveLockfile(paths.LockfilePath, lock); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return &PluginRemoveResult{ID: id}, nil
}

// PluginVerifyEntry is one plugin's verification outcome.
type PluginVerifyEntry struct {
	ID              string
	OK              bool
	NonReproducible bool
	Error           string
}

// PluginVerifyResult is the full `plect plugin verify` report.
type PluginVerifyResult struct {
	Entries []PluginVerifyEntry
	AllOK   bool
}

// PluginVerify re-resolves every enabled plugin across every registered
// catalog and compares it against plect.lock, entirely from local state.
// lockedOnly skips plugins whose catalog is an editable path source — they
// are never pinned or verified, per the plugin-packaging design.
func PluginVerify(paths PluginPaths, lockedOnly bool) (*PluginVerifyResult, error) {
	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	result := &PluginVerifyResult{AllOK: true}
	for _, entry := range registrations.Catalogs {
		rc, catalogErr := plugins.VerifyAndMountCatalog(entry, lock, paths.CacheRoot)
		for _, path := range entry.Plugins {
			id := entry.Alias + "/" + path
			if lockedOnly && catalogErr == nil && rc.NonReproducible {
				continue
			}
			pluginEntry := PluginVerifyEntry{ID: id, NonReproducible: catalogErr == nil && rc.NonReproducible}
			switch {
			case catalogErr != nil:
				pluginEntry.Error = catalogErr.Error()
			default:
				if _, err := plugins.VerifyAndMountPlugin(rc, path, paths.CacheRoot, lock, version.Current); err != nil {
					pluginEntry.Error = err.Error()
				} else {
					pluginEntry.OK = true
				}
			}
			if pluginEntry.Error != "" && !pluginEntry.NonReproducible {
				result.AllOK = false
			}
			result.Entries = append(result.Entries, pluginEntry)
		}
	}
	return result, nil
}

// PluginListEntry is one enabled plugin's declared/resolved/locked and
// compatibility state, for `plect plugin list`.
type PluginListEntry struct {
	ID               string
	CatalogSource    string
	ResolvedRevision string
	ContentHash      string
	Version          string
	PlectMinVersion  string
	NonReproducible  bool
	// Status is "ok" when the plugin resolves and mounts cleanly, otherwise
	// the load error's message.
	Status string
}

// PluginList reports every enabled plugin's state without mounting
// anything into a live config.
func PluginList(paths PluginPaths) ([]PluginListEntry, error) {
	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	var entries []PluginListEntry
	for _, entry := range registrations.Catalogs {
		rc, catalogErr := plugins.VerifyAndMountCatalog(entry, lock, paths.CacheRoot)
		for _, path := range entry.Plugins {
			id := entry.Alias + "/" + path
			locked, _ := lock.FindPlugin(id)
			e := PluginListEntry{
				ID:               id,
				CatalogSource:    entry.Source,
				ResolvedRevision: locked.CatalogResolvedRevision,
				ContentHash:      locked.ContentHash,
				Version:          locked.Version,
				PlectMinVersion:  locked.PlectMinVersion,
				NonReproducible:  catalogErr == nil && rc.NonReproducible,
				Status:           "ok",
			}
			switch {
			case catalogErr != nil:
				e.Status = catalogErr.Error()
			default:
				if _, err := plugins.VerifyAndMountPlugin(rc, path, paths.CacheRoot, lock, version.Current); err != nil {
					e.Status = err.Error()
				}
			}
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// splitPluginRef parses a catalog-qualified plugin identity
// "<catalog-alias>/<path>" without requiring the path to already be
// enabled — `plugin add` is the act of enabling it, so it can't reuse
// CatalogRegistrations.SplitPluginID, which checks enabled-list membership.
func splitPluginRef(id string) (alias, path string, err error) {
	alias, path, ok := strings.Cut(id, "/")
	if !ok || alias == "" || path == "" {
		return "", "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("plugin id %q must be \"<catalog-alias>/<path>\"", id)}
	}
	return alias, path, nil
}
