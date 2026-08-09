package github

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type URLType string

const (
	URLTypeIssue URLType = "issue"
	URLTypePR    URLType = "pr"
)

type ParsedURL struct {
	Type      URLType
	Owner     string
	Repo      string
	OwnerRepo string
	Number    int
}

var (
	issueRe = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	prRe    = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
)

func ParseURL(url string) (*ParsedURL, error) {
	if m := issueRe.FindStringSubmatch(url); m != nil {
		num, _ := strconv.Atoi(m[3])
		return &ParsedURL{
			Type:      URLTypeIssue,
			Owner:     m[1],
			Repo:      m[2],
			OwnerRepo: m[1] + "/" + m[2],
			Number:    num,
		}, nil
	}
	if m := prRe.FindStringSubmatch(url); m != nil {
		num, _ := strconv.Atoi(m[3])
		return &ParsedURL{
			Type:      URLTypePR,
			Owner:     m[1],
			Repo:      m[2],
			OwnerRepo: m[1] + "/" + m[2],
			Number:    num,
		}, nil
	}
	return nil, fmt.Errorf("invalid GitHub URL: %s\nExpected: https://github.com/<owner>/<repo>/issues/<number>\n      or https://github.com/<owner>/<repo>/pull/<number>", url)
}

// URL reconstructs the GitHub URL from the parsed components.
func (p *ParsedURL) URL() string {
	if p.Type == URLTypePR {
		return fmt.Sprintf("https://github.com/%s/pull/%d", p.OwnerRepo, p.Number)
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", p.OwnerRepo, p.Number)
}

func SessionName(ownerRepo string, number int) string {
	return fmt.Sprintf("%s-%d", ownerRepo, number)
}

// IsURL returns true if the string looks like a URL (starts with https://).
func IsURL(s string) bool {
	return strings.HasPrefix(s, "https://")
}

// IsProjectItemID returns true if the string looks like a GitHub Projects v2
// item ID (e.g. "PVTI_xxx").
func IsProjectItemID(s string) bool {
	return strings.HasPrefix(s, "PVTI_")
}

// SessionNameWithTag returns a session name with a tag appended.
// e.g. "owner/repo-79" + "review" → "owner/repo-79+review"
func SessionNameWithTag(ownerRepo string, number int, tag string) string {
	return fmt.Sprintf("%s-%d+%s", ownerRepo, number, tag)
}

// BranchWithTag appends a tag to a branch name.
// e.g. "issue/79" + "review" → "issue/79+review"
func BranchWithTag(branch, tag string) string {
	return branch + "+" + tag
}

func SanitizeBranch(branch string) string {
	r := strings.NewReplacer("/", "-", ":", "-", "+", "-")
	return r.Replace(branch)
}
