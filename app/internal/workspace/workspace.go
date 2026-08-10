package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gh "github.com/kecbigmt/sennit/app/internal/github"
	"github.com/kecbigmt/sennit/app/internal/procexec"
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
	// runner executes git/gh child processes. Tests substitute a fake
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

// ResolveBranch resolves the branch name for the given parsed URL. ctx bounds
// the `gh pr view` invocation issued for PR URLs; issue URLs derive the
// branch name locally and never shell out.
func (m *Manager) ResolveBranch(ctx context.Context, parsed *gh.ParsedURL) (string, error) {
	if parsed.Type == gh.URLTypePR {
		out, _, err := m.runnerOrDefault().Run(ctx, "", false, "gh", "pr", "view", fmt.Sprintf("%d", parsed.Number),
			"--repo", parsed.OwnerRepo,
			"--json", "headRefName", "--jq", ".headRefName")
		if err != nil {
			return "", fmt.Errorf("failed to get PR info (is gh authenticated?): %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	return fmt.Sprintf("issue/%d", parsed.Number), nil
}

// RepoDir returns the repository root directory for the given owner/repo.
func (m *Manager) RepoDir(ownerRepo string) string {
	return filepath.Join(m.WorktreesRoot, "github.com", ownerRepo)
}

// WorktreePath returns the worktree path for the given owner/repo and branch.
func (m *Manager) WorktreePath(ownerRepo, branch string) string {
	sanitized := gh.SanitizeBranch(branch)
	return filepath.Join(m.WorktreesRoot, "github.com", ownerRepo, sanitized)
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
	// Primary checkout: <srcRoot>/<host>/<owner>/<repo>/.git as a directory
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

// AddParams holds parameters for workspace Add operation.
type AddParams struct {
	Parsed      *gh.ParsedURL
	Branch      string // target branch (may include suffix)
	BaseBranch  string // original branch before suffix (empty = same as Branch)
	SessionName string // session name (may include suffix)
}

// Add creates a worktree and returns workspace info. ctx bounds every git
// invocation Add issues (fetch, worktree add); a cancelled or expired ctx
// terminates the in-flight process and Add returns its error.
func (m *Manager) Add(ctx context.Context, params AddParams) (*WorkspaceInfo, error) {
	parsed := params.Parsed
	branch := params.Branch
	baseBranch := params.BaseBranch
	if baseBranch == "" {
		baseBranch = branch
	}
	sessionName := params.SessionName

	repoDir := m.RepoDir(parsed.OwnerRepo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("repository not found at %s\nClone it first", repoDir)
	}

	gitDir, err := m.FindGitDir(repoDir)
	if err != nil {
		return nil, err
	}

	wtPath := m.WorktreePath(parsed.OwnerRepo, branch)

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
		if parsed.Type == gh.URLTypePR {
			// Try fetching the branch by name first; if the remote branch has been
			// deleted (e.g. merged PR), fall back to the PR ref.
			if err := m.runGit(ctx, gitDir, "fetch", "origin", baseBranch); err != nil {
				prRef := fmt.Sprintf("pull/%d/head:%s", parsed.Number, baseBranch)
				if err2 := m.runGit(ctx, gitDir, "fetch", "origin", prRef); err2 != nil {
					return nil, fmt.Errorf("git fetch failed (branch: %v, PR ref: %w)", err, err2)
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
				// When branch == baseBranch, the PR ref fetch above created a local
				// branch named baseBranch (via pull/<number>/head:<baseBranch>),
				// so "git worktree add wtPath branch" works even without a remote ref.
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
				defaultBranch := m.resolveDefaultBranch(ctx, gitDir)
				if stderr, err := m.runGitCapture(ctx, gitDir, "worktree", "add", "-b", branch, wtPath, "origin/"+defaultBranch); err != nil {
					return nil, worktreeAddError(err, stderr)
				}
			}
		}
	}

	return info, nil
}

// Remove removes a worktree and optionally deletes the branch. ctx bounds
// every git invocation Remove issues.
// If the worktree directory is already gone, it uses `git worktree prune` to clean up stale entries.
func (m *Manager) Remove(ctx context.Context, parsed *gh.ParsedURL, branch string, force, deleteBranch bool) error {
	wtPath := m.WorktreePath(parsed.OwnerRepo, branch)
	repoDir := m.RepoDir(parsed.OwnerRepo)

	// Exclude the worktree being removed so FindGitDir doesn't return it as gitDir.
	gitDir, err := m.FindGitDir(repoDir, wtPath)
	if err != nil {
		return err
	}

	if err := m.removeWorktree(ctx, gitDir, wtPath, force); err != nil {
		return err
	}

	if deleteBranch {
		if err := m.runGit(ctx, gitDir, "branch", "-D", branch); err != nil {
			return fmt.Errorf("git branch delete failed: %w", err)
		}
	}

	return nil
}

// RemoveByPath removes a worktree by its path (for use when no URL is
// provided). ctx bounds every git invocation it issues.
func (m *Manager) RemoveByPath(ctx context.Context, wtPath, gitDir, branch string, force, deleteBranch bool) error {
	if err := m.removeWorktree(ctx, gitDir, wtPath, force); err != nil {
		return err
	}

	if deleteBranch {
		if err := m.runGit(ctx, gitDir, "branch", "-D", branch); err != nil {
			return fmt.Errorf("git branch delete failed: %w", err)
		}
	}

	return nil
}

// WorktreeExists checks if a worktree path exists.
func (m *Manager) WorktreeExists(ownerRepo, branch string) bool {
	wtPath := m.WorktreePath(ownerRepo, branch)
	_, err := os.Stat(wtPath)
	return err == nil
}

// removeWorktree removes a worktree, handling the case where the directory is already gone
// or the worktree is not registered with git.
func (m *Manager) removeWorktree(ctx context.Context, gitDir, wtPath string, force bool) error {
	_, pathErr := os.Stat(wtPath)
	pathExists := pathErr == nil

	if pathExists && m.isRegisteredWorktree(ctx, gitDir, wtPath) {
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
	} else if pathExists {
		// Directory exists but is not a registered worktree — remove it directly
		if err := os.RemoveAll(wtPath); err != nil {
			return fmt.Errorf("failed to remove directory %s: %w", wtPath, err)
		}
	} else {
		// Directory is gone — prune stale worktree entries
		_ = m.runGit(ctx, gitDir, "worktree", "prune")
	}
	return nil
}

// isRegisteredWorktree checks if wtPath is listed in `git worktree list`.
func (m *Manager) isRegisteredWorktree(ctx context.Context, gitDir, wtPath string) bool {
	out, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	cleanPath := filepath.Clean(wtPath)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			registered := filepath.Clean(strings.TrimPrefix(line, "worktree "))
			if registered == cleanPath {
				return true
			}
		}
	}
	return false
}

// worktreeAddError wraps a git worktree add error with a user-friendly message.
// stderr is checked separately because runGit no longer embeds it into err
// (the user already saw it live), and the hint detection depends on git's
// human-readable message rather than the exit status.
func worktreeAddError(err error, stderr string) error {
	if strings.Contains(stderr, "already checked out") || strings.Contains(stderr, "already used by worktree") {
		return fmt.Errorf("git worktree add failed: %w\nHint: a worktree for this branch already exists. To start a separate session, use a tag:\n  sennit create <url> --tag <tag>", err)
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

func (m *Manager) resolveDefaultBranch(ctx context.Context, gitDir string) string {
	out, _, err := m.runnerOrDefault().Run(ctx, gitDir, false, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(string(out))
	return strings.TrimPrefix(ref, "refs/remotes/origin/")
}
