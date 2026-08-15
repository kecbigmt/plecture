package github

import (
	"fmt"
	"path"
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

// IssueBranch is the branch name an issue resource maps to. A pull request's
// branch instead comes from FetchPullMeta's HeadRef, since it requires an API
// call the issue case never needs.
func IssueBranch(number int) string {
	return fmt.Sprintf("issue/%d", number)
}
