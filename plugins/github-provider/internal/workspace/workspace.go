package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/procexec"
)

type WorkspaceInfo struct {
	SessionName    string `json:"session_name"`
	WorktreePath   string `json:"worktree_path"`
	Branch         string `json:"branch"`
	RepoDir        string `json:"repo_dir"`
	GitDir         string `json:"git_dir"`
	ReusedSession  bool   `json:"reused_session"`
	ReusedWorktree bool   `json:"reused_worktree"`
}

func (w *WorkspaceInfo) JSON() string {
	b, _ := json.MarshalIndent(w, "", "  ")
	return string(b)
}

type Manager struct {
	WorktreesRoot string
	// SrcRoot holds the primary checkouts (~/src). Empty falls back to
	// ~/src, so a Manager built by an older call site still resolves them.
	SrcRoot string
	// runner executes git child processes. Tests substitute a fake
	// binary via PATH rather than swapping this; it defaults to
	// procexec.Default so production Managers need not set it.
	runner procexec.Runner
}

func NewManager(worktreesRoot string) *Manager {
	return &Manager{WorktreesRoot: worktreesRoot, runner: procexec.Default}
}

// runner returns m.runner with its default applied, so a Manager built via
// struct literal (as some tests do) still works.
func (m *Manager) runnerOrDefault() procexec.Runner {
	if m.runner != nil {
		return m.runner
	}
	return procexec.Default
}

// srcRoot is SrcRoot with its default applied.
func (m *Manager) srcRoot() string {
	if m.SrcRoot != "" {
		return m.SrcRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "src")
}

// SrcDir maps a worktree container back to its primary checkout, mirroring
// the path convention repo-clone creates. Returns "" when repoDir is not
// under WorktreesRoot.
func (m *Manager) SrcDir(repoDir string) string {
	root := m.srcRoot()
	if root == "" || m.WorktreesRoot == "" {
		return ""
	}
	rel, err := filepath.Rel(m.WorktreesRoot, repoDir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.Join(root, rel)
}

// RepoDir returns the container directory holding every worktree of a
// repository. repo is the repository's path relative to the worktrees root,
// an opaque slug the caller supplies (plect never derives it from the shape
// of a resource identifier).
func (m *Manager) RepoDir(repo string) string {
	return filepath.Join(m.WorktreesRoot, filepath.FromSlash(repo))
}

// WorktreePath returns the worktree path for the given repository slug and
// branch.
func (m *Manager) WorktreePath(repo, branch string) string {
	return filepath.Join(m.RepoDir(repo), SanitizeBranch(branch))
}

// ContainerDir maps a worktree back to the repository container that holds
// it, which the path convention makes the worktree's parent directory. It
// exists so teardown can find the container from a recorded worktree path
// alone, without knowing how the repository slug was derived.
func ContainerDir(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	return filepath.Dir(worktreePath)
}

// SanitizeBranch turns a branch name into a single path segment by replacing
// the separators that would otherwise create nested directories or confuse
// path handling.
func SanitizeBranch(branch string) string {
	r := strings.NewReplacer("/", "-", ":", "-", "+", "-")
	return r.Replace(branch)
}

// FindGitDir resolves the directory to use as the `git -C` target for the repo,
// newest layout first.
//
// Primary checkout (preferred): the ~/src sibling of repoDir, an ordinary
// clone whose worktrees live under repoDir.
//
// Bare layout: if <repoDir>/.git is a gitfile and <repoDir>/.bare is a
// directory, returns repoDir itself — git resolves the gitfile transparently.
//
// Legacy fullclone layout: scans immediate subdirectories of repoDir and
// returns the first one containing a .git entry. excludePaths are skipped
// (useful to avoid returning a worktree about to be deleted).
//
// The latter two remain so sessions predating the ~/src migration keep
// resolving; they retire once no such repo layout is left on the machine.
func (m *Manager) FindGitDir(repoDir string, excludePaths ...string) (string, error) {
	// Primary checkout: <srcRoot>/<host>/<namespace>/<name>/.git as a directory
	if srcDir := m.SrcDir(repoDir); srcDir != "" {
		if st, err := os.Stat(filepath.Join(srcDir, ".git")); err == nil && st.IsDir() {
			return srcDir, nil
		}
	}

	// Bare layout: gitfile at <repoDir>/.git + bare dir at <repoDir>/.bare
	if st, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil && !st.IsDir() {
		if _, err := os.Stat(filepath.Join(repoDir, ".bare")); err == nil {
			return repoDir, nil
		}
	}

	excludeSet := make(map[string]bool, len(excludePaths))
	for _, p := range excludePaths {
		excludeSet[filepath.Clean(p)] = true
	}

	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return "", fmt.Errorf("cannot read repo directory %s: %w", repoDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(repoDir, e.Name())
		if excludeSet[filepath.Clean(candidate)] {
			continue
		}
		gitPath := filepath.Join(candidate, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			// .git can be a directory (regular repo) or a file (worktree)
			_ = info
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no existing worktree found in %s", repoDir)
}

// AddParams holds parameters for the workspace Add operation.
type AddParams struct {
	// Repo is the repository's path relative to the worktrees root.
	Repo        string
	Branch      string // target branch (may include suffix)
	BaseBranch  string // original branch before suffix (empty = same as Branch)
	SessionName string // session name (may include suffix)
	// FallbackRefspec is fetched when fetching BaseBranch by name fails,
	// which is how a caller acquires a branch whose remote head no longer
	// exists. Empty means the base branch is fetched from the remote in the
	// ordinary way and no fallback is attempted.
	FallbackRefspec string
}

// Add creates a worktree and returns workspace info. ctx bounds every git
// invocation Add issues (fetch, worktree add); a cancelled or expired ctx
// terminates the in-flight process and Add returns its error.
func (m *Manager) Add(ctx context.Context, params AddParams) (*WorkspaceInfo, error) {
	branch := params.Branch
	baseBranch := params.BaseBranch
	if baseBranch == "" {
		baseBranch = branch
	}
	sessionName := params.SessionName

	repoDir := m.RepoDir(params.Repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("repository not found at %s\nClone it first", repoDir)
	}

	gitDir, err := m.FindGitDir(repoDir)
	if err != nil {
		return nil, err
	}

	wtPath := m.WorktreePath(params.Repo, branch)

	info := &WorkspaceInfo{
		SessionName:  sessionName,
		WorktreePath: wtPath,
		Branch:       branch,
		RepoDir:      repoDir,
		GitDir:       gitDir,
	}

	// Check if worktree already exists
	if _, err := os.Stat(wtPath); err == nil {
		info.ReusedWorktree = true
	} else {
		if params.FallbackRefspec != "" {
			// Try fetching the branch by name first; if the remote branch has
			// been deleted, fall back to the refspec the caller supplied.
			if err := m.runGit(ctx, gitDir, "fetch", "origin", baseBranch); err != nil {
				if err2 := m.runGit(ctx, gitDir, "fetch", "origin", params.FallbackRefspec); err2 != nil {
					return nil, fmt.Errorf("git fetch failed (branch: %v, fallback refspec: %w)", err, err2)
				}
			}
			if branch != baseBranch {
				// Tag case. Reuse a tagged branch left by a prior session
				// (destroy removes the worktree but a non-force destroy may keep
				// the branch) so an orphan never blocks re-dispatch; otherwise
				// create it from the fetched base.
				if m.branchExists(ctx, gitDir, branch) {
					if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", wtPath, branch); err != nil {
						return nil, worktreeAddError(err, stderr)
					}
				} else {
					startPoint := "origin/" + baseBranch
					if !m.refExists(ctx, gitDir, startPoint) {
						startPoint = baseBranch // use local ref from PR fetch
					}
					if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", "-b", branch, wtPath, startPoint); err != nil {
						return nil, worktreeAddError(err, stderr)
					}
				}
			} else {
				// When branch == baseBranch, the fallback refspec fetched above
				// created a local branch named baseBranch, so
				// "git worktree add wtPath branch" works even without a remote ref.
				if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", wtPath, branch); err != nil {
					return nil, worktreeAddError(err, stderr)
				}
			}
		} else {
			if err := m.runGit(ctx, gitDir, "fetch", "origin"); err != nil {
				return nil, fmt.Errorf("git fetch failed: %w", err)
			}
			if m.branchExists(ctx, gitDir, branch) {
				// Branch already exists: reuse it
				if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", wtPath, branch); err != nil {
					return nil, worktreeAddError(err, stderr)
				}
			} else {
				// Branch does not exist: create new branch from default branch
				defaultBranch := m.resolveDefaultBranch(ctx, repoDir, gitDir)
				if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", "-b", branch, wtPath, "origin/"+defaultBranch); err != nil {
					return nil, worktreeAddError(err, stderr)
				}
			}
		}
	}

	return info, nil
}

// Remove removes the worktree of repo/branch and optionally deletes the
// branch. ctx bounds every git invocation Remove issues.
// If the worktree directory is already gone, it uses `git worktree prune` to clean up stale entries.
func (m *Manager) Remove(ctx context.Context, repo, branch string, force, deleteBranch bool) error {
	wtPath := m.WorktreePath(repo, branch)
	repoDir := m.RepoDir(repo)

	// Exclude the worktree being removed so FindGitDir doesn't return it as gitDir.
	gitDir, err := m.FindGitDir(repoDir, wtPath)
	if err != nil {
		return err
	}

	if err := m.removeWorktree(ctx, gitDir, wtPath, force); err != nil {
		return err
	}

	if deleteBranch {
		m.reclaimBranch(ctx, gitDir, branch)
	}

	return nil
}

// RemoveByPath removes a worktree by its path, for callers that already know
// where it lives. ctx bounds every git invocation it issues.
func (m *Manager) RemoveByPath(ctx context.Context, wtPath, gitDir, branch string, force, deleteBranch bool) error {
	if err := m.removeWorktree(ctx, gitDir, wtPath, force); err != nil {
		return err
	}

	if deleteBranch {
		m.reclaimBranch(ctx, gitDir, branch)
	}

	return nil
}

// reclaimBranch deletes the branch a worktree removal leaves behind, using a
// safe delete (`-d`, never `-D`) so a branch carrying commits `git` can't
// prove are merged is left in place rather than discarded. A refusal is not
// an error: the branch becomes an orphan a later dispatch on the same
// resource reuses (see Add's orphan-branch reuse path), so convergence holds
// either way cleanup responds to a safe-delete refusal.
func (m *Manager) reclaimBranch(ctx context.Context, gitDir, branch string) {
	_ = m.runGit(ctx, gitDir, "branch", "-d", branch)
}

// WorktreeExists checks if a worktree path exists.
func (m *Manager) WorktreeExists(repo, branch string) bool {
	wtPath := m.WorktreePath(repo, branch)
	_, err := os.Stat(wtPath)
	return err == nil
}

// removeWorktree removes a worktree, handling the case where the directory is already gone
// or the worktree is not registered with git.
func (m *Manager) removeWorktree(ctx context.Context, gitDir, wtPath string, force bool) error {
	_, pathErr := os.Stat(wtPath)
	pathExists := pathErr == nil

	if pathExists {
		registered, err := m.isRegisteredWorktree(ctx, gitDir, wtPath)
		if err != nil {
			// The registration check itself failed (including context
			// cancellation/timeout): we cannot tell whether wtPath is a real
			// worktree, so we must not fall through to a raw filesystem
			// delete — that would bypass git's own data-loss guard on an
			// operation we couldn't actually verify.
			return fmt.Errorf("failed to check worktree registration for %s: %w", wtPath, err)
		}

		if registered {
			// Normal case: directory exists and is registered
			args := []string{"worktree", "remove"}
			if force {
				args = append(args, "--force")
			}
			args = append(args, wtPath)
			if err := m.runGit(ctx, gitDir, args...); err != nil {
				// Without --force, propagate git's refusal; falling back to
				// os.RemoveAll would defeat its data-loss guard.
				if !force {
					return err
				}
				if rmErr := os.RemoveAll(wtPath); rmErr != nil {
					return fmt.Errorf("git worktree remove failed: %w (manual cleanup also failed: %v)", err, rmErr)
				}
				_ = m.runGit(ctx, gitDir, "worktree", "prune")
			}
		} else {
			// Directory exists but is not a registered worktree — remove it directly
			if err := os.RemoveAll(wtPath); err != nil {
				return fmt.Errorf("failed to remove directory %s: %w", wtPath, err)
			}
		}
	} else {
		// Directory is gone — prune stale worktree entries
		_ = m.runGit(ctx, gitDir, "worktree", "prune")
	}
	return nil
}

// isRegisteredWorktree reports whether wtPath is listed in `git worktree
// list`. A non-nil error (including one caused by ctx cancellation or
// timeout) means the check could not be performed at all — the caller must
// treat that as "unknown", never as "not registered".
func (m *Manager) isRegisteredWorktree(ctx context.Context, gitDir, wtPath string) (bool, error) {
	out, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	cleanPath := filepath.Clean(wtPath)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			registered := filepath.Clean(strings.TrimPrefix(line, "worktree "))
			if registered == cleanPath {
				return true, nil
			}
		}
	}
	return false, nil
}

// worktreeAddError wraps a git worktree add error with a user-friendly message.
// stderr is checked separately because runGit no longer embeds it into err
// (the user already saw it live), and the hint detection depends on git's
// human-readable message rather than the exit status.
func worktreeAddError(err error, stderr string) error {
	if strings.Contains(stderr, "already checked out") || strings.Contains(stderr, "already used by worktree") {
		return fmt.Errorf("git worktree add failed: %w\nHint: a worktree for this branch already exists. To start a separate session, use a tag:\n  plect up <resource> --tag <tag>", err)
	}
	return fmt.Errorf("git worktree add failed: %w", err)
}

// runGit streams git's stdout/stderr to the parent's stderr so the user sees
// progress and failure reasons in real time. The returned error is the raw
// exit-status — callers add their own context, which avoids the prefix
// stuttering you'd otherwise get in chains like "worktree add failed: git
// worktree add ...: exit status N". ctx bounds the invocation: cancellation
// or a deadline terminates the process and surfaces as the returned error.
func (m *Manager) runGit(ctx context.Context, dir string, args ...string) error {
	_, _, err := m.runnerOrDefault().Run(ctx, dir, true, "git", args...)
	return err
}

// runGitCapture is runGit plus stderr capture, for callers that need to inspect
// git's message (e.g. detecting "already checked out" to add a tag hint).
// Stderr is still streamed so the user sees it live.
func (m *Manager) runGitCapture(ctx context.Context, dir string, args ...string) (string, error) {
	_, stderr, err := m.runnerOrDefault().Run(ctx, dir, true, "git", args...)
	return string(stderr), err
}

func (m *Manager) branchExists(ctx context.Context, gitDir, branch string) bool {
	_, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (m *Manager) refExists(ctx context.Context, gitDir, ref string) bool {
	_, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// resolveDefaultBranch picks the branch new work starts from: a per-repo
// override recorded at <repoDir>/.plect/base-branch when present, otherwise
// the remote's actual default. The override is a plain file on the
// repository container rather than provider config because providers don't
// cascade to individual repo layers — a git-flow repo that branches from
// `develop`, not GitHub's own default, has no other place to record that.
func (m *Manager) resolveDefaultBranch(ctx context.Context, repoDir, gitDir string) string {
	if override, err := os.ReadFile(filepath.Join(repoDir, ".plect", "base-branch")); err == nil {
		if b := strings.TrimSpace(string(override)); b != "" {
			return b
		}
	}

	out, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(string(out))
	return strings.TrimPrefix(ref, "refs/remotes/origin/")
}
