// Package workspace implements the local-okf workspace provider's
// dispatch-time workspace acquisition: it turns a
// `local-okf://<owner>/<concept-id>` resource id and a session name into a
// read-context workspace directory over the owner's knowledge bundle. There
// is no git repository here and nothing to isolate — the workspace
// directory holds a symlink into the bundle, not a copy, so a dispatched
// session sees the concept file plus its siblings exactly as a human reading
// the bundle would.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/okf/internal/bundle"
)

// scratchDirName is the child directory scratch workspace directories live
// under, inside the orchestrator's own workspace directory. It is
// dot-prefixed to keep these transient child workspaces out of the
// orchestrator's own view when it reads its workspace directory as durable
// memory, and Cleanup refuses to remove anything whose resolved path does
// not contain this name — the one invariant standing between a corrupted
// `workspace_dir` output and an `rm -rf` of something that isn't a
// known-disposable scratch dir.
const scratchDirName = ".okf-workspaces"

// SetupResult is the workspace provider outputs contract: the reserved
// `workspace_dir` key plus the resource facts a workflow's templates may
// want.
type SetupResult struct {
	WorkspaceDir string
	Owner        string
	ConceptID    string
	ConceptPath  string
}

// Setup resolves the resource id to a concept file inside the owner's
// bundle, then declares a per-session scratch workspace directory holding a
// `knowledge` symlink into the bundle root. The workspace directory lives
// under the orchestrator's own workspace directory rather than a global
// scratch tree: placing it beside the bundle it reads keeps the
// relationship self-evident, and it means destroying the orchestrator
// reclaims every scratch dir it ever handed out along with it.
func Setup(runner bundle.StatusRunner, resourceID, sessionName string) (*SetupResult, error) {
	owner, conceptID, err := bundle.ParseResourceID(resourceID)
	if err != nil {
		return nil, err
	}

	workspaceDir, rerr := bundle.ResolveOwnerWorkspaceDir(runner, owner)
	if rerr != nil {
		return nil, rerr
	}
	root, rerr := bundle.Root(workspaceDir)
	if rerr != nil {
		return nil, rerr
	}
	conceptPath, rerr := bundle.ResolveConceptPath(root, conceptID)
	if rerr != nil {
		return nil, rerr
	}

	scratch := filepath.Join(workspaceDir, scratchDirName, sessionName)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, fmt.Errorf("create scratch workspace dir %s: %w", scratch, err)
	}
	symlink := filepath.Join(scratch, "knowledge")
	if err := os.Remove(symlink); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("replace stale knowledge symlink %s: %w", symlink, err)
	}
	if err := os.Symlink(root, symlink); err != nil {
		return nil, fmt.Errorf("link knowledge bundle into scratch workspace dir: %w", err)
	}

	return &SetupResult{
		WorkspaceDir: scratch,
		Owner:        owner,
		ConceptID:    conceptID,
		ConceptPath:  conceptPath,
	}, nil
}

// Cleanup removes the scratch dir and its symlink. A workspace directory
// that is already gone is treated as released, so destroy converges.
// Cleanup never touches the bundle itself: the workspace directory owns no
// content of its own beyond the symlink, so removing it removes nothing the
// bundle needs.
func Cleanup(workspaceDir string) error {
	if strings.TrimSpace(workspaceDir) == "" {
		return nil
	}
	if _, err := os.Lstat(workspaceDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	real, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		return fmt.Errorf("cleanup refused: workspace dir %q is not resolvable: %w", workspaceDir, err)
	}
	sep := string(os.PathSeparator)
	if !strings.Contains(real, sep+scratchDirName+sep) {
		return fmt.Errorf("cleanup refused: workspace dir %q is not under a %s/ directory; refusing to remove a path that isn't a known-disposable scratch dir", real, scratchDirName)
	}

	return os.RemoveAll(workspaceDir)
}
