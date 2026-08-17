// Package bundle implements the plumbing shared by every OKF concept kind:
// resolving the owner alias to an orchestrator workspace directory, locating
// that workspace directory's knowledge bundle, and containing a concept id
// inside it. Goal parsing itself lives in the sibling goal package; this
// package only knows how to find bytes safely, not how to interpret them.
package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ResolveError distinguishes a resource that simply cannot be located yet
// (Unresolved: no orchestrator session, an unreadable workspace directory, a
// bundle that hasn't been bootstrapped, or a missing file) from a resolution
// that must fail outright (an ambiguous owner alias, or a concept id that
// escapes the bundle). Observe folds the former into its UNRESOLVED state;
// every other caller (workspace provider setup, finalize) treats both the
// same way, as a hard error, because dispatch and completion recording have
// no "pending" state to fold into.
type ResolveError struct {
	Reason     string
	Unresolved bool
}

func (e *ResolveError) Error() string { return e.Reason }

// StatusRunner runs `plect status <alias> --json --full` and returns its raw
// stdout, or the combined output plus an error when the command exits
// non-zero. Tests supply a fake; production wires the real CLI.
type StatusRunner interface {
	Status(alias string) (output []byte, err error)
}

// statusPayload is the slice of `plect status --json --full` this package
// reads. Everything else in that payload belongs to callers that need it,
// not to bundle resolution.
type statusPayload struct {
	Runtime struct {
		WorkspaceDirPath   string `json:"workspace_dir_path"`
		WorkspaceDirExists bool   `json:"workspace_dir_exists"`
	} `json:"runtime"`
}

// ParseResourceID splits a `local-okf://<owner>/<concept-id>` resource
// identifier into its owner alias and concept id. The concept id is
// returned exactly as written — callers resolve it against a bundle root
// before treating it as a path.
func ParseResourceID(resourceID string) (owner, conceptID string, err error) {
	const scheme = "local-okf://"
	rest, ok := strings.CutPrefix(resourceID, scheme)
	if !ok {
		return "", "", &ResolveError{Reason: "unsupported resource identifier: " + resourceID + " (expected local-okf://<owner>/<concept-id>)"}
	}
	owner, conceptID, ok = strings.Cut(rest, "/")
	if !ok || owner == "" || conceptID == "" {
		return "", "", &ResolveError{Reason: "unsupported resource identifier: " + resourceID + " (expected local-okf://<owner>/<concept-id>)"}
	}
	return owner, conceptID, nil
}

// ResolveOwnerWorkspaceDir resolves the "owner:<owner>" orchestrator session
// alias to its workspace directory. An alias matching more than one session
// is a hard error: silently picking one would record a goal-file edit or a
// completion entry against the wrong bundle. Every other failure to
// resolve — no session, or a workspace directory that is unreadable, likely
// mid-destroy — is soft: the caller decides whether that means UNRESOLVED or
// a hard stop.
func ResolveOwnerWorkspaceDir(runner StatusRunner, owner string) (string, *ResolveError) {
	output, err := runner.Status("owner:" + owner)
	if err != nil {
		if strings.Contains(string(output), "matches multiple sessions") {
			return "", &ResolveError{Reason: string(output)}
		}
		return "", &ResolveError{
			Reason:     "no orchestrator session for \"owner:" + owner + "\" (not created, or destroy in progress)",
			Unresolved: true,
		}
	}

	var payload statusPayload
	if err := json.Unmarshal(output, &payload); err != nil {
		return "", &ResolveError{
			Reason:     "no orchestrator session for \"owner:" + owner + "\" (unparseable status output)",
			Unresolved: true,
		}
	}
	if payload.Runtime.WorkspaceDirPath == "" || !payload.Runtime.WorkspaceDirExists {
		return "", &ResolveError{
			Reason:     "orchestrator workspace directory \"" + payload.Runtime.WorkspaceDirPath + "\" is not readable (possibly mid-destroy)",
			Unresolved: true,
		}
	}
	return payload.Runtime.WorkspaceDirPath, nil
}

// Root resolves an orchestrator workspace directory's knowledge bundle to
// its real, symlink-free path. Every later containment check is anchored to
// this path, so it must itself be fully resolved and must fully exist — a
// bundle that hasn't been bootstrapped yet is UNRESOLVED, not a hard error.
func Root(workspaceDirPath string) (string, *ResolveError) {
	dir := filepath.Join(workspaceDirPath, "knowledge", "bundle")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", &ResolveError{
			Reason:     "knowledge bundle not found at " + dir,
			Unresolved: true,
		}
	}
	return real, nil
}

// ResolveConceptPath resolves a concept id against an already-resolved
// bundle root, refusing any resolution that would escape it. The check runs
// twice: once against the lexical path before the final component is known
// to exist (catching a symlinked ancestor directory that escapes the
// bundle), and once more after the final component is resolved (catching
// the final component itself being an escaping symlink). A concept id that
// simply doesn't exist yet is UNRESOLVED; an id that resolves outside the
// bundle is always a hard error, because honoring it would read or write
// outside the bundle the caller was authorized for.
func ResolveConceptPath(root, conceptID string) (string, *ResolveError) {
	if strings.HasPrefix(conceptID, "/") {
		return "", &ResolveError{Reason: "resolution refused: path escapes the knowledge bundle: concept id \"" + conceptID + "\" is absolute"}
	}

	lexical, err := realpathM(filepath.Join(root, conceptID))
	if err != nil {
		return "", &ResolveError{Reason: "resolution refused: " + err.Error()}
	}
	if !contains(root, lexical) {
		return "", &ResolveError{Reason: "resolution refused: path escapes the knowledge bundle: concept id \"" + conceptID + "\" resolves to \"" + lexical + "\" outside \"" + root + "\""}
	}

	if _, err := os.Lstat(lexical); err != nil {
		return "", &ResolveError{
			Reason:     "goal file not resolvable under " + root + ": " + err.Error(),
			Unresolved: true,
		}
	}

	real, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", &ResolveError{
			Reason:     "concept file not resolvable under " + root + ": " + err.Error(),
			Unresolved: true,
		}
	}
	if !contains(root, real) {
		return "", &ResolveError{Reason: "resolution refused: path escapes the knowledge bundle: concept id \"" + conceptID + "\" resolves to \"" + real + "\" outside \"" + root + "\""}
	}

	return real, nil
}

// contains reports whether path is root itself or lives under it. Both
// arguments must already be cleaned, symlink-resolved paths.
func contains(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

// realpathM mirrors GNU `realpath -m`: it resolves symlinks in the longest
// existing leading path, then lexically applies the remaining (possibly
// nonexistent) trailing components on top. This is what lets a symlinked
// ancestor directory be caught as an escape before the final path component
// is known to exist at all.
func realpathM(path string) (string, error) {
	clean := filepath.Clean(path)

	var trailing []string
	cur := clean
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		trailing = append([]string{filepath.Base(cur)}, trailing...)
		cur = parent
	}

	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{resolved}, trailing...)...), nil
}
