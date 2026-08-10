package github

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/kecbigmt/sennit/app/internal/procexec"
)

// RepoSlug is the repository's path relative to the worktrees root for a
// GitHub repository: the host segment plus owner/repo. The workspace manager
// treats this as an opaque slug, so knowing the host belongs here.
func RepoSlug(ownerRepo string) string {
	return path.Join("github.com", ownerRepo)
}

// PullRefspec is the refspec that fetches a pull request's head into a local
// branch. It is the fallback when the pull request's remote branch has been
// deleted, which happens once the pull request is merged.
func PullRefspec(number int, localBranch string) string {
	return fmt.Sprintf("pull/%d/head:%s", number, localBranch)
}

// ResolveBranch resolves the branch name for the given parsed URL. ctx bounds
// the `gh pr view` invocation issued for pull request URLs; issue URLs derive
// the branch name locally and never shell out.
func ResolveBranch(ctx context.Context, parsed *ParsedURL) (string, error) {
	if parsed.Type == URLTypePR {
		out, _, err := procexec.Default.Run(ctx, "", false, "gh", "pr", "view", fmt.Sprintf("%d", parsed.Number),
			"--repo", parsed.OwnerRepo,
			"--json", "headRefName", "--jq", ".headRefName")
		if err != nil {
			return "", fmt.Errorf("failed to get PR info (is gh authenticated?): %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	return fmt.Sprintf("issue/%d", parsed.Number), nil
}
