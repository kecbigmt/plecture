package github

import "testing"

func TestExpandIssueBranch(t *testing.T) {
	tests := []struct {
		name     string
		template string
		owner    string
		repo     string
		number   int
		want     string
	}{
		{name: "empty template falls back to the shipped default", number: 79, want: "issue/79"},
		{name: "declared default", template: "issue/{number}", number: 79, want: "issue/79"},
		{name: "team prefix", template: "work/issue-{number}", number: 7, want: "work/issue-7"},
		{name: "repository placeholders", template: "{owner}/{repo}/{number}", owner: "acme", repo: "widgets", number: 3, want: "acme/widgets/3"},
		{name: "an unknown placeholder is left alone", template: "issue/{nope}-{number}", number: 5, want: "issue/{nope}-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandIssueBranch(tt.template, tt.owner, tt.repo, tt.number); got != tt.want {
				t.Errorf("ExpandIssueBranch = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandTaggedBranch(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
		branch string
		tag    string
		want   string
	}{
		{name: "empty suffix falls back to the shipped default", branch: "issue/79", tag: "review", want: "issue/79+review"},
		{name: "declared default", suffix: "+{tag}", branch: "issue/79", tag: "review", want: "issue/79+review"},
		{name: "slash-separated tags", suffix: "/{tag}", branch: "issue/79", tag: "review", want: "issue/79/review"},
		{name: "suffix without the tag placeholder is used verbatim", suffix: "-wip", branch: "issue/79", tag: "review", want: "issue/79-wip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandTaggedBranch(tt.suffix, tt.branch, tt.tag); got != tt.want {
				t.Errorf("ExpandTaggedBranch = %q, want %q", got, tt.want)
			}
		})
	}
}
