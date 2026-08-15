package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

// FetchedCatalog is what fetching a catalog source produced, before any
// trust decision or persistence.
type FetchedCatalog struct {
	Root             string
	ResolvedRevision string // empty for path and path+editable schemes.
	Manifest         CatalogManifest
}

// FetchCatalog resolves source's content: a git catalog is cloned and
// checked out into cacheRoot, content-addressed by source digest + resolved
// commit SHA; a path or path+editable catalog is resolved via symlinks in
// place. FetchCatalog performs no trust check — `plect catalog add`'s flow
// fetches first so it can show the user what an as-yet-unregistered source
// resolves to before asking for confirmation. revision is required for a
// git source and ignored otherwise. subdir, when non-empty, is a catalog-
// relative subdirectory of the fetched source that becomes the catalog
// root — see ResolveCatalogSubdir.
func FetchCatalog(ctx context.Context, runner procexec.Runner, source, revision, subdir, cacheRoot string) (FetchedCatalog, error) {
	scheme, rest, err := ParseSource(source)
	if err != nil {
		return FetchedCatalog{}, err
	}

	var sourceRoot, resolvedRevision string
	switch {
	case scheme.IsGit():
		if revision == "" {
			return FetchedCatalog{}, fmt.Errorf("source %q: `--revision` is required for a git catalog", source)
		}
		sourceRoot, resolvedRevision, err = fetchGitCatalog(ctx, runner, source, rest, revision, cacheRoot)
	case scheme == SchemePath || scheme == SchemePathEditable:
		sourceRoot, _, err = resolvePathTree(rest)
	default:
		err = fmt.Errorf("source %q: unsupported scheme", source)
	}
	if err != nil {
		return FetchedCatalog{}, err
	}

	root, err := ResolveCatalogSubdir(sourceRoot, subdir)
	if err != nil {
		return FetchedCatalog{}, err
	}
	manifest, err := LoadCatalogManifest(root)
	if err != nil {
		return FetchedCatalog{}, err
	}
	return FetchedCatalog{Root: root, ResolvedRevision: resolvedRevision, Manifest: manifest}, nil
}

// fetchGitCatalog clones+checks out into a staging directory, resolves the
// commit SHA, then moves it under its content-addressed final path keyed by
// source digest + that SHA. When the final path already exists (an earlier
// fetch produced the same snapshot), the staging clone is discarded rather
// than kept as a duplicate.
func fetchGitCatalog(ctx context.Context, runner procexec.Runner, source, transportURL, revision, cacheRoot string) (root, resolvedRevision string, err error) {
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("create %s: %w", cacheRoot, err)
	}
	staging, err := os.MkdirTemp(cacheRoot, ".fetch-*")
	if err != nil {
		return "", "", fmt.Errorf("create staging dir: %w", err)
	}
	// FetchGit refuses to clone into an existing directory, but MkdirTemp
	// always creates one — remove it first and let git recreate it.
	if err := os.Remove(staging); err != nil {
		return "", "", fmt.Errorf("prepare staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	resolvedRevision, err = FetchGit(ctx, runner, transportURL, revision, staging)
	if err != nil {
		return "", "", err
	}

	final := CacheDir(cacheRoot, source, resolvedRevision)
	if _, statErr := os.Stat(final); statErr == nil {
		return final, resolvedRevision, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", "", fmt.Errorf("create %s: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(staging, final); err != nil {
		return "", "", fmt.Errorf("move fetched content to %s: %w", final, err)
	}
	return final, resolvedRevision, nil
}
