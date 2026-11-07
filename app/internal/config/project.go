package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// UntrustedProjectError carries the canonical root, which is exactly the
// value to add to trusted_project_roots.
type UntrustedProjectError struct {
	Root string
}

func (e *UntrustedProjectError) Error() string {
	return fmt.Sprintf("project root %s is not trusted: its .plect/ definitions can run commands; add %q to trusted_project_roots in %s to use it", e.Root, e.Root, DefaultPath())
}

// FindProjectRoot returns a canonical path because that is the root's
// identity: a symlinked invocation and a direct one select the same root.
func FindProjectRoot(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	cur, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(projectMarkerPath(cur)); err == nil {
			return cur, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// SelectProjectRoot reports an untrusted root as an error rather than as the
// global-only context, so a missing trust decision never selects a global
// workflow of the same name.
func (c *Config) SelectProjectRoot(dir string) (string, error) {
	root, err := FindProjectRoot(dir)
	if err != nil || root == "" {
		return "", err
	}
	if !c.trustsProjectRoot(root) {
		return "", &UntrustedProjectError{Root: root}
	}
	return root, nil
}

// projectLayer checks a recorded root again on every load: trust is revocable
// and the marker removable, and neither may fall back to another context.
func (c *Config) projectLayer(root string) (layerDir, error) {
	if err := lang.LoadProjectMarker(projectMarkerPath(root)); err != nil {
		return layerDir{}, fmt.Errorf("project root %s: %w", root, err)
	}
	if !c.trustsProjectRoot(root) {
		return layerDir{}, &UntrustedProjectError{Root: root}
	}
	return layerDir{dir: filepath.Join(root, ".plect"), project: true}, nil
}

// RequireProjectRoot accepts the global-only context and a root that is
// still marked and trusted.
func (c *Config) RequireProjectRoot(root string) error {
	if root == "" {
		return nil
	}
	_, err := c.projectLayer(root)
	return err
}

func projectMarkerPath(root string) string {
	return filepath.Join(root, ".plect", lang.ProjectMarkerFileName)
}

// trustsProjectRoot compares canonical paths, so a listed symlink and its
// target trust the same root.
func (c *Config) trustsProjectRoot(dir string) bool {
	want := canonicalPath(dir)
	for _, root := range c.TrustedProjectRoots {
		if canonicalPath(root) == want {
			return true
		}
	}
	return false
}

func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
