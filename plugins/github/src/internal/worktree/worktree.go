// Package worktree implements the GitHub workspace provider's lifecycle: it
// turns a GitHub issue or pull request identifier plus a session name into
// an acquired git worktree, and releases that worktree again on cleanup.
//
// Everything GitHub-specific lives here: how a resource identifier is
// parsed, which branch a resource maps to, and how a repository's worktrees
// are laid out under the workspace-dirs root.
package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/workspace"
)

// WorktreeManager is the git worktree lifecycle surface Setup and Cleanup use.
type WorktreeManager interface {
	Add(context.Context, workspace.AddParams) (*workspace.WorkspaceInfo, error)
	RemoveByPath(context.Context, string, string, string, bool, bool) error
	FindGitDir(string, ...string) (string, error)
}

// SetupOptions are the inputs the workspace provider setup hook receives.
type SetupOptions struct {
	// ResourceID is the canonical resource identifier: a GitHub issue or
	// pull request URL, or a Projects v2 item id that resolves to one.
	ResourceID string
	// SessionName is the session the workspace is acquired for. Its
	// "<name>+<tag>" suffix, when present, is what separates one tool's
	// workspace on a resource from another's.
	SessionName string
	// WorkspaceDirsRoot overrides the configured workspace-dirs root.
	WorkspaceDirsRoot string
	// Manager lets tests observe acquisition without a real repository.
	Manager WorktreeManager
	// GHClient fetches title/state metadata. Defaults to ghapi.Direct(); a
	// mounted plugin's workspace provider hook instead passes one bound to
	// the bundled github-watcher binary, so setup shares its shared rate
	// budget (see workspaces/github.toml's setup hook).
	GHClient github.GHClient
}

// Setup acquires the workspace for a GitHub resource and returns the outputs
// the workspace provider contract requires on stdout: the reserved
// `workspace_dir` key plus the resource facts a workflow's templates may
// want.
func Setup(ctx context.Context, opts SetupOptions) (map[string]any, error) {
	parsed, err := resolve(ctx, opts.ResourceID)
	if err != nil {
		return nil, err
	}

	client := opts.GHClient
	if client == nil {
		client = ghapi.Direct()
	}

	var baseBranch, title, state string
	if parsed.Type == github.URLTypePR {
		meta, err := github.FetchPullMeta(ctx, client, parsed.OwnerRepo, parsed.Number)
		if err != nil {
			return nil, err
		}
		baseBranch, title, state = meta.HeadRef, meta.Title, meta.State
	} else {
		baseBranch = github.IssueBranch(parsed.Number)
		meta := github.FetchIssueMeta(ctx, client, parsed.OwnerRepo, parsed.Number)
		title, state = meta.Title, meta.State
	}

	branch := baseBranch
	if tag := SessionTag(opts.SessionName); tag != "" {
		branch = github.BranchWithTag(baseBranch, tag)
	}

	mgr := opts.Manager
	if mgr == nil {
		mgr = workspace.NewManager(workspaceDirsRoot(opts.WorkspaceDirsRoot))
	}
	params := workspace.AddParams{
		Repo:        github.RepoSlug(parsed.OwnerRepo),
		Branch:      branch,
		BaseBranch:  baseBranch,
		SessionName: opts.SessionName,
	}
	if parsed.Type == github.URLTypePR {
		params.FallbackRefspec = github.PullRefspec(parsed.Number, baseBranch)
	}
	info, err := mgr.Add(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("acquire worktree: %w", err)
	}
	if info.WorktreePath == "" {
		return nil, fmt.Errorf("acquired worktree path is empty")
	}

	outputs := map[string]any{
		"workspace_dir": info.WorktreePath,
		"branch":        branch,
		"url":           parsed.URL(),
		"owner_repo":    parsed.OwnerRepo,
		"owner":         parsed.Owner,
		"repo":          parsed.Repo,
		"number":        parsed.Number,
	}
	if title != "" {
		outputs["title"] = title
	}
	if state != "" {
		if parsed.Type == github.URLTypePR {
			outputs["pr_state"] = state
		} else {
			outputs["issue_state"] = state
		}
	}
	return outputs, nil
}

// CleanupOptions are the inputs the workspace provider cleanup hook
// receives, read back from the outputs setup recorded.
type CleanupOptions struct {
	WorkspaceDir string
	Branch       string
	// Force removes the worktree even when it carries uncommitted changes,
	// mirroring the caller's `plect destroy --force` intent.
	Force bool
	// DeleteBranch reclaims the branch setup created, mirroring the caller's
	// `plect destroy --input delete_branch=true` intent
	// (workspaces/github.toml's cleanup hook). Opt-in, not derived from
	// Branch being non-empty: a branch left behind after worktree removal is
	// the safer default, since deleting one is not always what a
	// review-only or shared-branch session wants.
	DeleteBranch bool
	// WorkspaceDirsRoot overrides the configured workspace-dirs root.
	WorkspaceDirsRoot string
	// Manager lets tests observe release without a real repository.
	Manager WorktreeManager
}

// Cleanup releases the worktree setup acquired, reclaiming its branch too
// when DeleteBranch opts in. A setup that never produced a workspace leaves
// nothing to release, which is a success rather than an error: cleanup must
// converge on a session whose setup failed halfway.
func Cleanup(ctx context.Context, opts CleanupOptions) error {
	if strings.TrimSpace(opts.WorkspaceDir) == "" {
		return nil
	}
	mgr := opts.Manager
	if mgr == nil {
		mgr = workspace.NewManager(workspaceDirsRoot(opts.WorkspaceDirsRoot))
	}
	container := workspace.ContainerDir(opts.WorkspaceDir)
	gitDir, err := mgr.FindGitDir(container, opts.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("release worktree: %w", err)
	}
	if err := mgr.RemoveByPath(ctx, opts.WorkspaceDir, gitDir, opts.Branch, opts.Force, opts.DeleteBranch); err != nil {
		return fmt.Errorf("release worktree: %w", err)
	}
	return nil
}

// SessionTag extracts the session tag from a session name's
// "<name>+<tag>" convention. An untagged name yields the empty string.
func SessionTag(sessionName string) string {
	if idx := strings.LastIndex(sessionName, "+"); idx >= 0 {
		return sessionName[idx+1:]
	}
	return ""
}

// resolve turns a resource identifier into parsed GitHub coordinates,
// querying the API only for a Projects v2 item id, which carries no
// repository or number of its own.
func resolve(ctx context.Context, resource string) (*github.ParsedURL, error) {
	if github.IsProjectItemID(resource) {
		return github.ResolveProjectItemID(ctx, resource)
	}
	return github.ParseURL(resource)
}

func workspaceDirsRoot(override string) string {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "workspace_dirs")
	if override != "" {
		root = override
	}
	if strings.HasPrefix(root, "~/") {
		root = filepath.Join(home, root[2:])
	}
	return root
}
