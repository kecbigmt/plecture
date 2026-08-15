// Package workspace implements the local-okf provider's dispatch-time
// workspace acquisition: it turns a `local-okf://<owner>/<concept-id>`
// resource id and a session name into a read-context workdir over the
// owner's knowledge bundle. There is no git repository here and nothing to
// isolate — the workdir holds a symlink into the bundle, not a copy, so a
// dispatched session sees the concept file plus its siblings exactly as a
// human reading the bundle would.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/okf/internal/bundle"
)

// scratchDirName is the child directory scratch workdirs live under,
// inside the orchestrator's own workdir. It is dot-prefixed to keep these
// transient child workspaces out of the orchestrator's own view when it
// reads its workdir as durable memory, and Cleanup refuses to remove
// anything whose resolved path does not contain this name — the one
// invariant standing between a corrupted `workdir` output and an `rm -rf`
// of something that isn't a known-disposable scratch dir.
const scratchDirName = ".okf-workspaces"

// SetupResult is the provider outputs contract: the reserved `workdir` key
// plus the resource facts a workflow's templates may want.
type SetupResult struct {
	Workdir     string
	Owner       string
	ConceptID   string
	ConceptPath string
}

// Setup resolves the resource id to a concept file inside the owner's
// bundle, then declares a per-session scratch workdir holding a `knowledge`
// symlink into the bundle root. The workdir lives under the orchestrator's
// own workdir rather than a global scratch tree: placing it beside the
// bundle it reads keeps the relationship self-evident, and it means
// destroying the orchestrator reclaims every scratch dir it ever handed
// out along with it.
func Setup(runner bundle.StatusRunner, resourceID, sessionName string) (*SetupResult, error) {
	owner, conceptID, err := bundle.ParseResourceID(resourceID)
	if err != nil {
		return nil, err
	}

	workdir, rerr := bundle.ResolveOwnerWorkdir(runner, owner)
	if rerr != nil {
		return nil, rerr
	}
	root, rerr := bundle.Root(workdir)
	if rerr != nil {
		return nil, rerr
	}
	conceptPath, rerr := bundle.ResolveConceptPath(root, conceptID)
	if rerr != nil {
		return nil, rerr
	}

	scratch := filepath.Join(workdir, scratchDirName, sessionName)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, fmt.Errorf("create scratch workdir %s: %w", scratch, err)
	}
	symlink := filepath.Join(scratch, "knowledge")
	if err := os.Remove(symlink); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("replace stale knowledge symlink %s: %w", symlink, err)
	}
	if err := os.Symlink(root, symlink); err != nil {
		return nil, fmt.Errorf("link knowledge bundle into scratch workdir: %w", err)
	}

	return &SetupResult{
		Workdir:     scratch,
		Owner:       owner,
		ConceptID:   conceptID,
		ConceptPath: conceptPath,
	}, nil
}

// Cleanup removes the scratch dir and its symlink. A workdir that is
// already gone is treated as released, so destroy converges. Cleanup never
// touches the bundle itself: the workdir owns no content of its own beyond
// the symlink, so removing it removes nothing the bundle needs.
func Cleanup(workdir string) error {
	if strings.TrimSpace(workdir) == "" {
		return nil
	}
	if _, err := os.Lstat(workdir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	real, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return fmt.Errorf("cleanup refused: workdir %q is not resolvable: %w", workdir, err)
	}
	sep := string(os.PathSeparator)
	if !strings.Contains(real, sep+scratchDirName+sep) {
		return fmt.Errorf("cleanup refused: workdir %q is not under a %s/ directory; refusing to remove a path that isn't a known-disposable scratch dir", real, scratchDirName)
	}

	return os.RemoveAll(workdir)
}
