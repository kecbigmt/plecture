package github

import (
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    *ParsedURL
		wantErr bool
	}{
		{
			name: "issue URL",
			url:  "https://github.com/acme/widgets/issues/10",
			want: &ParsedURL{
				Type:      URLTypeIssue,
				Owner:     "acme",
				Repo:      "widgets",
				OwnerRepo: "acme/widgets",
				Number:    10,
			},
		},
		{
			name: "PR URL",
			url:  "https://github.com/acme/widgets/pull/44",
			want: &ParsedURL{
				Type:      URLTypePR,
				Owner:     "acme",
				Repo:      "widgets",
				OwnerRepo: "acme/widgets",
				Number:    44,
			},
		},
		{
			name: "PR URL with trailing path",
			url:  "https://github.com/org/repo/pull/123/files",
			want: &ParsedURL{
				Type:      URLTypePR,
				Owner:     "org",
				Repo:      "repo",
				OwnerRepo: "org/repo",
				Number:    123,
			},
		},
		{
			name:    "invalid URL",
			url:     "https://github.com/example/repo",
			wantErr: true,
		},
		{
			name:    "not github URL",
			url:     "https://gitlab.com/org/repo/issues/1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseURL() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("ParseURL() unexpected error: %v", err)
				return
			}
			if got.Type != tt.want.Type || got.Owner != tt.want.Owner || got.Repo != tt.want.Repo || got.Number != tt.want.Number || got.OwnerRepo != tt.want.OwnerRepo {
				t.Errorf("ParseURL() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSanitizeBranch(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feature/branch", "feature-branch"},
		{"issue/123", "issue-123"},
		{"fix:bug", "fix-bug"},
		{"issue/123+review", "issue-123-review"},
		{"main", "main"},
	}
	for _, tt := range tests {
		got := SanitizeBranch(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeBranch(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"https://github.com/org/repo/issues/1", true},
		{"https://github.com/org/repo/pull/1", true},
		{"https://not-github.com/org/repo", true},
		{"owner/repo-123", false},
		{"exampleorg/quri-336", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		got := IsURL(tt.input)
		if got != tt.want {
			t.Errorf("IsURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsProjectItemID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"PVTI_lAHOAZb6ss4BU9rHzgqYmsI", true},
		{"PVTI_abc", true},
		{"https://github.com/org/repo/issues/1", false},
		{"owner/repo-123", false},
		{"PVT_abc", false},
		{"pvti_abc", false},
	}
	for _, tt := range tests {
		got := IsProjectItemID(tt.input)
		if got != tt.want {
			t.Errorf("IsProjectItemID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSessionName(t *testing.T) {
	got := SessionName("acme/widgets", 10)
	if got != "acme/widgets-10" {
		t.Errorf("SessionName() = %q, want %q", got, "acme/widgets-10")
	}
}

func TestSessionNameWithTag(t *testing.T) {
	tests := []struct {
		ownerRepo string
		number    int
		tag       string
		want      string
	}{
		{"exampleorg/quri", 336, "review", "exampleorg/quri-336+review"},
		{"exampleorg/quri", 336, "debug", "exampleorg/quri-336+debug"},
		{"acme/widgets", 79, "test", "acme/widgets-79+test"},
	}
	for _, tt := range tests {
		got := SessionNameWithTag(tt.ownerRepo, tt.number, tt.tag)
		if got != tt.want {
			t.Errorf("SessionNameWithTag(%q, %d, %q) = %q, want %q", tt.ownerRepo, tt.number, tt.tag, got, tt.want)
		}
	}
}
