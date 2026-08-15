package plugins

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HashTree returns a deterministic "sha256:<hex>" digest of dir's regular
// file contents, keyed by path so a rename is detected as a change even if
// no byte differs. ".git" is excluded: it is present in a freshly cloned git
// source but never in a path source's tree, so including it would make the
// same logical content hash differently by resolution scheme.
func HashTree(dir string) (string, error) {
	return HashTreeExcluding(dir, nil)
}

// HashTreeExcluding is HashTree with a set of dir-relative paths omitted
// from the digest. The plugin resolution pipeline uses this to keep a
// plugin's content hash stable across a local `build` step (see
// BuildOutputPaths): a build's output is a machine-specific derivative of
// the trusted source, not itself part of what the source revision pins, so
// it must not perturb the hash that revision is verified against.
func HashTreeExcluding(dir string, exclude []string) (string, error) {
	excluded := make(map[string]bool, len(exclude))
	for _, p := range exclude {
		excluded[filepath.Clean(p)] = true
	}

	var rels []string
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		if excluded[filepath.Clean(rel)] {
			return nil
		}
		rels = append(rels, rel)
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Strings(rels)

	h := sha256.New()
	for _, rel := range rels {
		full := filepath.Join(dir, rel)
		info, err := os.Lstat(full)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", full, err)
		}
		// A symlink's target is not hashed, only that a symlink exists at this
		// path: reading through it here could escape dir, and its target is
		// already covered by whatever real file it resolves to within dir.
		fmt.Fprintf(h, "%s\x00", rel)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return "", fmt.Errorf("readlink %s: %w", full, err)
			}
			fmt.Fprintf(h, "symlink:%s\x00", target)
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", full, err)
		}
		_, copyErr := io.Copy(h, f)
		f.Close()
		if copyErr != nil {
			return "", fmt.Errorf("read %s: %w", full, copyErr)
		}
		h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
