// Package provider implements the GitHub provider's working-directory
// lifecycle: it turns a GitHub issue or pull request identifier plus a
// session name into an acquired git worktree, and releases that worktree
// again on cleanup.
//
// Everything GitHub-specific lives here: how a resource identifier is
// parsed, which branch a resource maps to, and how a repository's worktrees
// are laid out under the workdirs root.
package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github-provider/internal/github"
	"github.com/kecbigmt/plecture/plugins/github-provider/internal/workspace"
)

// WorkdirManager is the git workdir lifecycle surface Setup and Cleanup use.
type WorkdirManager interface {
	Add(context.Context, workspace.AddParams) (*workspace.WorkspaceInfo, error)
	RemoveByPath(context.Context, string, string, string, bool, bool) error
	FindGitDir(string, ...string) (string, error)
}

// SetupOptions are the inputs the provider setup hook receives.
type SetupOptions struct {
	// ResourceID is the canonical resource identifier: a GitHub issue or
	// pull request URL, or a Projects v2 item id that resolves to one.
	ResourceID string
	// SessionName is the session the workdir is acquired for. Its
	// "<name>+<tag>" suffix, when present, is what separates one tool's
	// workdir on a resource from another's.
	SessionName string
	// WorkdirsRoot overrides the configured workdirs root.
	WorkdirsRoot string
	// Manager lets tests observe acquisition without a real repository.
	Manager WorkdirManager
	// GHClient fetches title/state metadata. Defaults to ghapi.Direct(); a
	// mounted plugin's provider hook instead passes one bound to the
	// bundled github-watcher binary, so setup shares its shared rate budget
	// (see providers/github.toml's setup hook).
	GHClient github.GHClient
}

// Setup acquires the working directory for a GitHub resource and returns the
// outputs the provider contract requires on stdout: the reserved `workdir`
// key plus the resource facts a workflow's templates may want.
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
		mgr = workspace.NewManager(workdirsRoot(opts.WorkdirsRoot))
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
		return nil, fmt.Errorf("acquire workdir: %w", err)
	}
	if info.WorktreePath == "" {
		return nil, fmt.Errorf("acquired workdir path is empty")
	}

	outputs := map[string]any{
		"workdir":    info.WorktreePath,
		"branch":     branch,
		"url":        parsed.URL(),
		"owner_repo": parsed.OwnerRepo,
		"owner":      parsed.Owner,
		"repo":       parsed.Repo,
		"number":     parsed.Number,
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

// CleanupOptions are the inputs the provider cleanup hook receives, read back
// from the outputs setup recorded.
type CleanupOptions struct {
	Workdir string
	Branch  string
	// Force removes the workdir even when it carries uncommitted changes,
	// mirroring the caller's `plect destroy --force` intent.
	Force bool
	// WorkdirsRoot overrides the configured workdirs root.
	WorkdirsRoot string
	// Manager lets tests observe release without a real repository.
	Manager WorkdirManager
}

// Cleanup releases the workdir setup acquired, reclaiming its branch so a
// later dispatch on the same resource is not blocked by an orphan. A setup
// that never produced a working directory leaves nothing to release, which
// is a success rather than an error: cleanup must converge on a session
// whose setup failed halfway.
func Cleanup(ctx context.Context, opts CleanupOptions) error {
	if strings.TrimSpace(opts.Workdir) == "" {
		return nil
	}
	mgr := opts.Manager
	if mgr == nil {
		mgr = workspace.NewManager(workdirsRoot(opts.WorkdirsRoot))
	}
	container := workspace.ContainerDir(opts.Workdir)
	gitDir, err := mgr.FindGitDir(container, opts.Workdir)
	if err != nil {
		return fmt.Errorf("release workdir: %w", err)
	}
	if err := mgr.RemoveByPath(ctx, opts.Workdir, gitDir, opts.Branch, opts.Force, opts.Branch != ""); err != nil {
		return fmt.Errorf("release workdir: %w", err)
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

func workdirsRoot(override string) string {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "workdirs")
	if override != "" {
		root = override
	}
	if strings.HasPrefix(root, "~/") {
		root = filepath.Join(home, root[2:])
	}
	return root
}
