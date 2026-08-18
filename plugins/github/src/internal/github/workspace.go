package github

import (
	"fmt"
	"path"
	"strconv"
	"strings"
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

// DefaultIssueBranchTemplate and DefaultTaggedBranchSuffix are what the
// naming parameters fall back to, so a workflow that declares neither gets
// the shipped convention.
const (
	DefaultIssueBranchTemplate = "issue/{number}"
	DefaultTaggedBranchSuffix  = "+{tag}"
)

// ExpandIssueBranch renders an issue's branch name from the author-declared
// naming template. Placeholders are single-brace and expanded here rather
// than being Go templates: the value travels through plect's own hook
// rendering first, where `{{...}}` would be consumed before this executable
// ever saw it.
func ExpandIssueBranch(template, owner, repo string, number int) string {
	if template == "" {
		template = DefaultIssueBranchTemplate
	}
	return strings.NewReplacer(
		"{owner}", owner,
		"{repo}", repo,
		"{number}", strconv.Itoa(number),
	).Replace(template)
}

// ExpandTaggedBranch appends a session tag to a branch using the
// author-declared suffix, which carries its own separator so a team can key
// tagged worktrees off something other than "+".
func ExpandTaggedBranch(suffix, branch, tag string) string {
	if suffix == "" {
		suffix = DefaultTaggedBranchSuffix
	}
	return branch + strings.ReplaceAll(suffix, "{tag}", tag)
}
