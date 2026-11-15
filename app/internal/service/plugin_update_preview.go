package service

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/pluginservice"
)

type PluginFileChange struct {
	Path   string
	Kind   string
	Change string
}

type PluginUpdatePreview struct {
	ID                 string
	ResolvedRevision   string
	OldTreeUnavailable bool
	Changes            []PluginFileChange
	ServicesToRestart  []string
}

func previewPluginUpdate(paths PluginPaths, id, path string, entry plugins.CatalogEntry, lock *plugins.Lockfile, fetched *plugins.FetchedCatalog, scheme plugins.Scheme) (PluginUpdatePreview, error) {
	newDir := filepath.Join(fetched.Root, path)
	newManifest, err := plugins.LoadManifest(newDir)
	if err != nil {
		return PluginUpdatePreview{}, err
	}
	newHash, err := plugins.HashTreeExcluding(newDir, plugins.BuildOutputPaths(newManifest))
	if err != nil {
		return PluginUpdatePreview{}, err
	}
	oldEntry, locked := lock.FindPlugin(id)
	oldDir, oldManifest, available := oldPluginTree(paths, entry, path, oldEntry, locked, scheme, newDir)
	excluded := plugins.BuildOutputPaths(newManifest)
	if available {
		excluded = append(excluded, plugins.BuildOutputPaths(*oldManifest)...)
	}
	if !available {
		oldDir = ""
	}
	changes, err := comparePluginTrees(oldDir, newDir, excluded, oldManifest, &newManifest)
	if err != nil {
		return PluginUpdatePreview{}, err
	}
	preview := PluginUpdatePreview{ID: id, ResolvedRevision: fetched.ResolvedRevision, OldTreeUnavailable: !available, Changes: changes}
	if !scheme.IsEditable() && (!locked || oldEntry.ContentHash != newHash) {
		mounted := []plugins.Mounted{{ID: id, Dir: newDir, Manifest: newManifest}}
		decls, _ := pluginservice.BuildDeclarationsFrom(filepath.Dir(paths.CatalogsPath), mounted, nil)
		for _, decl := range decls {
			if decl.PluginID == id {
				preview.ServicesToRestart = append(preview.ServicesToRestart, decl.Name)
			}
		}
		sort.Strings(preview.ServicesToRestart)
	}
	return preview, nil
}

func oldPluginTree(paths PluginPaths, entry plugins.CatalogEntry, path string, locked plugins.PluginLockEntry, hasLock bool, scheme plugins.Scheme, newDir string) (string, *plugins.Manifest, bool) {
	if !hasLock || locked.ContentHash == "" || locked.CatalogSource != entry.Source || scheme.IsEditable() {
		return "", nil, false
	}
	oldDir := newDir
	if scheme.IsGit() {
		if locked.CatalogResolvedRevision == "" {
			return "", nil, false
		}
		root, err := plugins.ResolveCatalogSubdir(plugins.CacheDir(paths.CacheRoot, entry.Source, locked.CatalogResolvedRevision), entry.Subdir)
		if err != nil {
			return "", nil, false
		}
		oldDir = filepath.Join(root, path)
	}
	manifest, err := plugins.LoadManifest(oldDir)
	if err != nil {
		return "", nil, false
	}
	hash, err := plugins.HashTreeExcluding(oldDir, plugins.BuildOutputPaths(manifest))
	if err != nil || hash != locked.ContentHash {
		return "", nil, false
	}
	return oldDir, &manifest, true
}

func comparePluginTrees(oldDir, newDir string, excluded []string, oldManifest, newManifest *plugins.Manifest) ([]PluginFileChange, error) {
	oldFiles := map[string]string{}
	var err error
	if oldDir != "" {
		oldFiles, err = pluginTreeFiles(oldDir, excluded)
		if err != nil {
			return nil, err
		}
	}
	newFiles, err := pluginTreeFiles(newDir, excluded)
	if err != nil {
		return nil, err
	}
	executablePaths := map[string]bool{}
	for _, manifest := range []*plugins.Manifest{oldManifest, newManifest} {
		if manifest == nil {
			continue
		}
		for _, executable := range manifest.Executables {
			executablePaths[filepath.Clean(executable.Path)] = true
		}
	}
	var changes []PluginFileChange
	for path, current := range newFiles {
		before, exists := oldFiles[path]
		if exists && before == current {
			continue
		}
		change := "added"
		if oldDir == "" {
			change = "changed"
		} else if exists {
			change = "modified"
		}
		changes = append(changes, PluginFileChange{Path: path, Kind: pluginFileKind(path, executablePaths), Change: change})
	}
	for path := range oldFiles {
		if _, exists := newFiles[path]; !exists {
			changes = append(changes, PluginFileChange{Path: path, Kind: pluginFileKind(path, executablePaths), Change: "removed"})
		}
	}
	kinds := map[string]int{"executables": 0, "scripts and sources": 1, "toml": 2, "prompts and docs": 3}
	sort.Slice(changes, func(i, j int) bool {
		if kinds[changes[i].Kind] != kinds[changes[j].Kind] {
			return kinds[changes[i].Kind] < kinds[changes[j].Kind]
		}
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func pluginFileKind(path string, executablePaths map[string]bool) string {
	if executablePaths[path] {
		return "executables"
	}
	if strings.HasSuffix(path, ".toml") {
		return "toml"
	}
	if strings.HasPrefix(path, "prompts/") || strings.HasPrefix(path, "docs/") || strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx") {
		return "prompts and docs"
	}
	return "scripts and sources"
}

func pluginTreeFiles(dir string, excluded []string) (map[string]string, error) {
	skip := map[string]bool{}
	for _, path := range excluded {
		skip[filepath.Clean(path)] = true
	}
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || skip[rel] {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files[rel] = "symlink:" + target
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("plugin file %s is not regular", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files[rel] = fmt.Sprintf("file:%x", h.Sum(nil))
		return nil
	})
	return files, err
}
