package plugins

import (
	"fmt"
	"path/filepath"
)

// Mounted is one catalog-qualified plugin resolved and verified, ready to
// be mounted as a read-only config base layer.
type Mounted struct {
	ID              string
	Dir             string
	Manifest        Manifest
	NonReproducible bool
}

// ErrMissingCatalogLock is returned when a non-editable catalog has no
// corresponding plect.lock record.
type ErrMissingCatalogLock struct{ Alias string }

func (e *ErrMissingCatalogLock) Error() string {
	return fmt.Sprintf("catalog %q: no plect.lock entry; run `plect catalog add` or `plect catalog update`", e.Alias)
}

// ErrCatalogSourceDrift is returned when catalogs.toml's declared source no
// longer matches what plect.lock recorded — a human hand-edited the
// registration without re-running catalog add/update.
type ErrCatalogSourceDrift struct{ Alias string }

func (e *ErrCatalogSourceDrift) Error() string {
	return fmt.Sprintf("catalog %q: declared source does not match plect.lock; run `plect catalog update`", e.Alias)
}

// ErrMissingPluginLock is returned when a non-editable plugin has no
// corresponding plect.lock entry.
type ErrMissingPluginLock struct{ ID string }

func (e *ErrMissingPluginLock) Error() string {
	return fmt.Sprintf("plugin %q: no plect.lock entry; run `plect plugin add` or `plect plugin update`", e.ID)
}

// ErrIncompatible is returned when the running plect version does not
// satisfy a plugin's plect_min_version.
type ErrIncompatible struct{ ID, Required, Running string }

func (e *ErrIncompatible) Error() string {
	return fmt.Sprintf("plugin %q requires plect >= %s, running %s", e.ID, e.Required, e.Running)
}

// ResolvedCatalog is a catalog root resolved from local state and validated
// against its own catalog.toml. For a git catalog, Root is only the
// snapshot the shared catalog lock record currently points at — it is used
// to validate catalog.toml's `plugins` listing, never as a plugin's mount
// point: `plugin update` repoints one plugin's own lock entry without
// touching its siblings, so two plugins from the same catalog can be
// legitimately pinned to different commits at once (see
// VerifyAndMountPlugin, which re-resolves per plugin for a git source).
type ResolvedCatalog struct {
	Alias           string
	Source          string
	Root            string
	Manifest        CatalogManifest
	NonReproducible bool
}

// VerifyAndMountCatalog resolves entry to a validated catalog root, entirely
// from local state: it never fetches over the network, so it is safe and
// fast to run on every config load. A registration itself is the trust act
// (catalogs.toml only ever contains what a human confirmed through `plect
// catalog add`), so this performs no separate trust check — only content
// verification against plect.lock, except for an editable path catalog,
// which is explicitly non-reproducible.
func VerifyAndMountCatalog(entry CatalogEntry, lock *Lockfile, cacheRoot string) (ResolvedCatalog, error) {
	scheme, rest, err := ParseSource(entry.Source)
	if err != nil {
		return ResolvedCatalog{}, err
	}

	if scheme.IsEditable() {
		root, _, err := resolvePathTree(rest)
		if err != nil {
			return ResolvedCatalog{}, err
		}
		manifest, err := LoadCatalogManifest(root)
		if err != nil {
			return ResolvedCatalog{}, err
		}
		return ResolvedCatalog{Alias: entry.Alias, Source: entry.Source, Root: root, Manifest: manifest, NonReproducible: true}, nil
	}

	record, ok := lock.FindCatalog(entry.Alias)
	if !ok {
		return ResolvedCatalog{}, &ErrMissingCatalogLock{Alias: entry.Alias}
	}
	if record.CatalogSource != entry.Source {
		return ResolvedCatalog{}, &ErrCatalogSourceDrift{Alias: entry.Alias}
	}

	var root string
	switch {
	case scheme.IsGit():
		if record.CatalogResolvedRevision == "" {
			return ResolvedCatalog{}, &ErrMissingCatalogLock{Alias: entry.Alias}
		}
		root = CacheDir(cacheRoot, entry.Source, record.CatalogResolvedRevision)
	case scheme == SchemePath:
		root, _, err = resolvePathTree(rest)
		if err != nil {
			return ResolvedCatalog{}, err
		}
	default:
		return ResolvedCatalog{}, fmt.Errorf("catalog %q: source %q: unsupported scheme", entry.Alias, entry.Source)
	}

	manifest, err := LoadCatalogManifest(root)
	if err != nil {
		return ResolvedCatalog{}, err
	}
	return ResolvedCatalog{Alias: entry.Alias, Source: entry.Source, Root: root, Manifest: manifest}, nil
}

// VerifyAndMountPlugin resolves one plugin at pluginPath belonging to an
// already-resolved catalog, verifying its own lock entry's content hash
// against the mounted directory — local-only, Load()-time safe. The caller
// is responsible for having checked pluginPath is listed in
// catalog.Manifest.Plugins (VerifyAndMountAll does).
//
// For a non-editable git catalog, the plugin's mount directory is resolved
// from its OWN lock entry's CatalogResolvedRevision, via cacheRoot —
// deliberately never from catalog.Root. `plugin update` repoints only the
// target plugin's lock entry, so a sibling plugin from the same catalog can
// still be pinned to an older commit; reusing catalog.Root here would mount
// (and verify) every plugin from whatever snapshot the catalog's shared
// lock record currently points at, silently drifting siblings onto commits
// they were never verified against.
func VerifyAndMountPlugin(catalog ResolvedCatalog, pluginPath string, cacheRoot string, lock *Lockfile, currentPlectVersion string) (Mounted, error) {
	id := catalog.Alias + "/" + pluginPath

	if catalog.NonReproducible {
		dir := filepath.Join(catalog.Root, pluginPath)
		m, err := LoadManifest(dir)
		if err != nil {
			return Mounted{}, err
		}
		return finishPluginMount(id, dir, m, true, currentPlectVersion)
	}

	entry, ok := lock.FindPlugin(id)
	if !ok {
		return Mounted{}, &ErrMissingPluginLock{ID: id}
	}

	scheme, _, err := ParseSource(catalog.Source)
	if err != nil {
		return Mounted{}, err
	}
	pluginCatalogRoot := catalog.Root
	if scheme.IsGit() {
		if entry.CatalogResolvedRevision == "" {
			return Mounted{}, &ErrMissingPluginLock{ID: id}
		}
		pluginCatalogRoot = CacheDir(cacheRoot, catalog.Source, entry.CatalogResolvedRevision)
	}
	dir := filepath.Join(pluginCatalogRoot, pluginPath)

	m, err := LoadManifest(dir)
	if err != nil {
		return Mounted{}, err
	}
	hash, err := HashTreeExcluding(dir, BuildOutputPaths(m))
	if err != nil {
		return Mounted{}, fmt.Errorf("plugin %q: %w", id, err)
	}
	if hash != entry.ContentHash {
		return Mounted{}, &ErrHashMismatch{Path: dir, Want: entry.ContentHash, Got: hash}
	}
	return finishPluginMount(id, dir, m, false, currentPlectVersion)
}

func finishPluginMount(id, dir string, m Manifest, nonReproducible bool, currentPlectVersion string) (Mounted, error) {
	satisfied, err := AtLeast(currentPlectVersion, m.PlectMinVersion)
	if err != nil {
		return Mounted{}, fmt.Errorf("plugin %q: %w", id, err)
	}
	if !satisfied {
		return Mounted{}, &ErrIncompatible{ID: id, Required: m.PlectMinVersion, Running: currentPlectVersion}
	}
	return Mounted{ID: id, Dir: dir, Manifest: m, NonReproducible: nonReproducible}, nil
}

// VerifyAndMountAll resolves every registered catalog and its enabled
// plugins, entirely from local state, in declaration order.
func VerifyAndMountAll(registrations *CatalogRegistrations, lock *Lockfile, cacheRoot, currentPlectVersion string) ([]Mounted, error) {
	var out []Mounted
	for _, entry := range registrations.Catalogs {
		rc, err := VerifyAndMountCatalog(entry, lock, cacheRoot)
		if err != nil {
			return nil, fmt.Errorf("catalog %q: %w", entry.Alias, err)
		}
		for _, path := range entry.Plugins {
			if !containsString(rc.Manifest.Plugins, path) {
				return nil, fmt.Errorf("catalog %q: enabled plugin path %q is not listed in catalog.toml", entry.Alias, path)
			}
			mounted, err := VerifyAndMountPlugin(rc, path, cacheRoot, lock, currentPlectVersion)
			if err != nil {
				return nil, err
			}
			out = append(out, mounted)
		}
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
