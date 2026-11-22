package main

import (
	"reflect"
	"strings"
	"testing"
)

const repo = "acme/widgets"

const pinned = "https://raw.githubusercontent.com/acme/widgets/0123456789abcdef0123456789abcdef01234567/docs/history/changes/1-x/overview.svg"

func rules(findings []Finding) []string {
	out := []string{}
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func TestCheck_ClosingKeyword(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"closes", "Closes #12\n", nil},
		{"lowercase fixes", "This fixes #12.\n", nil},
		{"resolved with colon", "Resolved: #12\n", nil},
		{"same-repo qualified", "Closes acme/widgets#12\n", nil},
		{"same-repo url", "Closes https://github.com/acme/widgets/issues/12\n", nil},
		{"repo case differs", "Closes Acme/Widgets#12\n", nil},
		{"inside emphasis", "**Closes #12**\n", nil},
		{"in a list item", "- Closes #12\n", nil},
		{"one of several", "Part of #3.\n\nCloses #12\n", nil},
		{"keyword across a soft line break", "This change closes\n#12\n", nil},
		{"empty body", "", []string{"closing-keyword"}},
		{"bare reference", "Part of #12\n", []string{"closing-keyword"}},
		{"prose that is not a keyword", "Follow-up to #10, closing #12.\n", []string{"closing-keyword"}},
		{"keyword without a number", "Closes the gap.\n", []string{"closing-keyword"}},
		{"keyword glued to a word", "Encloses #12\n", []string{"closing-keyword"}},
		{"other repo", "Closes other/repo#12\n", []string{"closing-keyword"}},
		{"other repo url", "Closes https://github.com/other/repo/issues/12\n", []string{"closing-keyword"}},
		{"code span", "Run `Closes #12` later.\n", []string{"closing-keyword"}},
		{"fenced code", "```\nCloses #12\n```\n", []string{"closing-keyword"}},
		{"indented code", "Text.\n\n    Closes #12\n", []string{"closing-keyword"}},
		{"html comment", "<!-- Closes #12 -->\n", []string{"closing-keyword"}},
		{"inline html comment", "See <!-- Closes #12 --> above.\n", []string{"closing-keyword"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rules(Check([]byte(tt.body), repo))
			want := tt.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Check(%q) rules = %v, want %v", tt.body, got, want)
			}
		})
	}
}

func TestCheck_OverviewImages(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		want     []string
		wantLine int
	}{
		{"pinned image", "## Overview\n\n![d](" + pinned + ")\n", nil, 0},
		{"no images", "## Overview\n\nProse only.\n", nil, 0},
		{"no overview", "## What\n\n![d](https://example.com/a.svg)\n", nil, 0},
		{"unpinned image after the section ends", "## Overview\n\n![d](" + pinned + ")\n\n## Verification\n\n![s](https://example.com/s.png)\n", nil, 0},
		{"pinned html img", "## Overview\n\n<img src=\"" + pinned + "\" alt=\"d\">\n", nil, 0},
		{"pinned reference image", "## Overview\n\n![d][o]\n\n[o]: " + pinned + "\n", nil, 0},
		{"branch ref", "## Overview\n\n![d](https://raw.githubusercontent.com/acme/widgets/main/docs/o.svg)\n", []string{"overview-image-sha"}, 3},
		{"short sha", "## Overview\n\n![d](https://raw.githubusercontent.com/acme/widgets/0123456/docs/o.svg)\n", []string{"overview-image-sha"}, 3},
		{"other repo", "## Overview\n\n![d](https://raw.githubusercontent.com/other/repo/0123456789abcdef0123456789abcdef01234567/o.svg)\n", []string{"overview-image-sha"}, 3},
		{"other host", "## Overview\n\n![d](https://example.com/0123456789abcdef0123456789abcdef01234567/o.svg)\n", []string{"overview-image-sha"}, 3},
		{"empty alt text", "## Overview\n\nText.\n![](https://example.com/o.svg)\n", []string{"overview-image-sha"}, 4},
		{"nested in a list", "## Overview\n\n- item\n  ![d](https://example.com/o.svg)\n", []string{"overview-image-sha"}, 4},
		{"subsection stays inside", "## Overview\n\n### Change\n\n![d](https://example.com/o.svg)\n", []string{"overview-image-sha"}, 5},
		{"unpinned html img", "## Overview\n\n<p>\n<img src=\"https://example.com/o.svg\">\n</p>\n", []string{"overview-image-sha"}, 4},
		{"unpinned inline html img", "## Overview\n\nSee <img src=\"https://example.com/o.svg\"> here.\n", []string{"overview-image-sha"}, 3},
		{"reference image", "## Overview\n\n![d][o]\n\n[o]: https://example.com/o.svg\n", []string{"overview-image-sha"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "Closes #1\n\n" + tt.body
			findings := Check([]byte(body), repo)
			want := tt.want
			if want == nil {
				want = []string{}
			}
			if got := rules(findings); !reflect.DeepEqual(got, want) {
				t.Fatalf("rules = %v, want %v (findings %+v)", got, want, findings)
			}
			if tt.wantLine != 0 && findings[0].Line != tt.wantLine+2 {
				t.Fatalf("line = %d, want %d", findings[0].Line, tt.wantLine+2)
			}
		})
	}
}

func TestCheck_Allow(t *testing.T) {
	const unpinned = "![d](https://example.com/o.svg)"
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			"allow suppresses a missing closing keyword",
			"Part of #3.\n\n<!-- check-pr-body: allow closing-keyword because the issue stays open for the second PR -->\n",
			nil,
		},
		{
			"inline allow suppresses a missing closing keyword",
			"Part of #3. <!-- check-pr-body: allow closing-keyword because no issue tracks this hotfix -->\n",
			nil,
		},
		{
			"allow on the image line suppresses that image",
			"Closes #1\n\n## Overview\n\n" + unpinned + " <!-- check-pr-body: allow overview-image-sha because the diagram is generated per run -->\n",
			nil,
		},
		{
			"allow on another line does not suppress the image",
			"Closes #1\n\n## Overview\n\n<!-- check-pr-body: allow overview-image-sha because the diagram is generated per run -->\n\n" + unpinned + "\n",
			[]string{"stale-allow", "overview-image-sha"},
		},
		{
			"allow with nothing to suppress",
			"Closes #1\n\n<!-- check-pr-body: allow closing-keyword because no issue tracks this hotfix -->\n",
			[]string{"stale-allow"},
		},
		{
			"allow without a reason",
			"Part of #3.\n\n<!-- check-pr-body: allow closing-keyword -->\n",
			[]string{"closing-keyword", "malformed-allow"},
		},
		{
			"allow naming an unknown rule",
			"Closes #1\n\n<!-- check-pr-body: allow everything because reasons -->\n",
			[]string{"malformed-allow"},
		},
		{
			"allow inside a code block is not an allow",
			"Part of #3.\n\n```\n<!-- check-pr-body: allow closing-keyword because quoted -->\n```\n",
			[]string{"closing-keyword"},
		},
		{
			"unrelated comment is ignored",
			"Closes #1\n\n<!-- drafted with a template -->\n",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Check([]byte(tt.body), repo)
			want := tt.want
			if want == nil {
				want = []string{}
			}
			if got := rules(findings); !reflect.DeepEqual(got, want) {
				t.Fatalf("rules = %v, want %v (findings %+v)", got, want, findings)
			}
		})
	}
}

func TestCheck_MessagesNameTheFixBeforeTheException(t *testing.T) {
	body := "Part of #3.\n\n## Overview\n\n![d](https://example.com/o.svg)\n"
	findings := Check([]byte(body), repo)
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want two", findings)
	}
	for _, f := range findings {
		fix := strings.Index(f.Message, "Fix:")
		exception := strings.Index(f.Message, "Exception:")
		if fix < 0 || exception < fix {
			t.Errorf("%s message %q must state the fix, then the exception syntax last", f.Rule, f.Message)
		}
	}
}

func TestCheck_LineNumbers(t *testing.T) {
	body := "Part of #3.\n\n<!-- check-pr-body: allow overview-image-sha because unused -->\n"
	findings := Check([]byte(body), repo)
	want := []Finding{
		{Line: 1, Rule: "closing-keyword"},
		{Line: 3, Rule: "stale-allow"},
	}
	if len(findings) != len(want) {
		t.Fatalf("findings = %+v, want %d", findings, len(want))
	}
	for i := range want {
		if findings[i].Line != want[i].Line || findings[i].Rule != want[i].Rule {
			t.Errorf("finding %d = %d %s, want %d %s", i, findings[i].Line, findings[i].Rule, want[i].Line, want[i].Rule)
		}
	}
}
