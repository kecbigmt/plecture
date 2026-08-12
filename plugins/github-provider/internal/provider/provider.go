// Package provider implements the GitHub provider's working-directory
// lifecycle: it turns a GitHub issue or pull request identifier plus a
// session name into an acquired git worktree, and releases that worktree
// again on cleanup.
//
// Everything GitHub-specific lives here — how a resource identifier is
// parsed, which branch a resource maps to, and how a repository's worktrees
// are laid out under the worktrees root. The generic half (creating and
// removing the worktree itself) is delegated to the plecture CLI's workspace
// subcommands, so this package never reimplements branch reuse, fetch
// fallback, or primary-checkout resolution.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/github"
)

// PlectureBinary is the plecture CLI the workspace calls are issued against. It
// is a variable so tests can point it at a stub.
var PlectureBinary = "plecture"

// Runner executes a command and returns its stdout. It exists so the tests
// can observe the workspace calls without a real plecture binary or a real
// repository.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// SetupOptions are the inputs the provider setup hook receives.
type SetupOptions struct {
	// ResourceID is the canonical resource identifier: a GitHub issue or
	// pull request URL, or a Projects v2 item id that resolves to one.
	ResourceID string
	// SessionName is the session the worktree is acquired for. Its
	// "<name>+<tag>" suffix, when present, is what separates one tool's
	// workspace on a resource from another's.
	SessionName string
	// Runner executes the plecture workspace calls. Nil uses the real CLI.
	Runner Runner
}

// Setup acquires the working directory for a GitHub resource and returns the
// outputs the provider contract requires on stdout: the reserved `workdir`
// key plus the resource facts a workflow's templates may want.
func Setup(ctx context.Context, opts SetupOptions) (map[string]any, error) {
	parsed, err := resolve(ctx, opts.ResourceID)
	if err != nil {
		return nil, err
	}
	baseBranch, err := github.ResolveBranch(ctx, parsed)
	if err != nil {
		return nil, err
	}
	branch := baseBranch
	if tag := SessionTag(opts.SessionName); tag != "" {
		branch = github.BranchWithTag(baseBranch, tag)
	}

	args := []string{"workspace", "add",
		"--repo", github.RepoSlug(parsed.OwnerRepo),
		"--branch", branch,
		"--base-branch", baseBranch,
		"--session", opts.SessionName,
	}
	if parsed.Type == github.URLTypePR {
		args = append(args, "--fallback-refspec", github.PullRefspec(parsed.Number, baseBranch))
	}
	out, err := run(ctx, opts.Runner, args...)
	if err != nil {
		return nil, fmt.Errorf("acquire worktree: %w", err)
	}
	var info struct {
		WorktreePath string `json:"worktree_path"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse workspace details: %w", err)
	}
	if info.WorktreePath == "" {
		return nil, fmt.Errorf("workspace details carried no worktree path")
	}

	return map[string]any{
		"workdir":    info.WorktreePath,
		"branch":     branch,
		"url":        parsed.URL(),
		"owner_repo": parsed.OwnerRepo,
		"owner":      parsed.Owner,
		"repo":       parsed.Repo,
		"number":     parsed.Number,
	}, nil
}

// CleanupOptions are the inputs the provider cleanup hook receives, read back
// from the outputs setup recorded.
type CleanupOptions struct {
	Workdir string
	Branch  string
	// Force removes the worktree even when it carries uncommitted changes,
	// mirroring the caller's `plecture destroy --force` intent.
	Force bool
	// Runner executes the plecture workspace calls. Nil uses the real CLI.
	Runner Runner
}

// Cleanup releases the worktree setup acquired, reclaiming its branch so a
// later dispatch on the same resource is not blocked by an orphan. A setup
// that never produced a working directory leaves nothing to release, which
// is a success rather than an error: cleanup must converge on a session
// whose setup failed halfway.
func Cleanup(ctx context.Context, opts CleanupOptions) error {
	if strings.TrimSpace(opts.Workdir) == "" {
		return nil
	}
	args := []string{"workspace", "remove", "--path", opts.Workdir}
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch, "--delete-branch")
	}
	if opts.Force {
		args = append(args, "--force")
	}
	if _, err := run(ctx, opts.Runner, args...); err != nil {
		return fmt.Errorf("release worktree: %w", err)
	}
	return nil
}

// SessionTag extracts the workspace tag from a session name's
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

func run(ctx context.Context, runner Runner, args ...string) ([]byte, error) {
	if runner == nil {
		runner = defaultRunner
	}
	return runner(ctx, PlectureBinary, args...)
}
