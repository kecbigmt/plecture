package plugins

import "testing"

func TestParseSource(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantScheme Scheme
		wantRest   string
	}{
		{"git https", "git+https://github.com/example/plect-plugins", SchemeGitHTTPS, "https://github.com/example/plect-plugins"},
		{"git ssh", "git+ssh://git@example.com/team/plect-catalog", SchemeGitSSH, "ssh://git@example.com/team/plect-catalog"},
		{"path", "path:///home/user/src/plect-catalog", SchemePath, "/home/user/src/plect-catalog"},
		{"path editable", "path+editable:///home/user/src/plect-catalog", SchemePathEditable, "/home/user/src/plect-catalog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, rest, err := ParseSource(tt.source)
			if err != nil {
				t.Fatalf("ParseSource(%q): unexpected error: %v", tt.source, err)
			}
			if scheme != tt.wantScheme {
				t.Errorf("scheme = %v, want %v", scheme, tt.wantScheme)
			}
			if rest != tt.wantRest {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

func TestParseSource_UnsupportedScheme(t *testing.T) {
	tests := []string{
		"archive+https://example.com/plugin.tar.gz",
		"https://github.com/example/plect-plugins",
		"",
		"git+http://example.com/insecure",
	}
	for _, source := range tests {
		if _, _, err := ParseSource(source); err == nil {
			t.Errorf("ParseSource(%q): want error, got nil", source)
		}
	}
}

func TestScheme_IsGit(t *testing.T) {
	if !SchemeGitHTTPS.IsGit() || !SchemeGitSSH.IsGit() {
		t.Error("git schemes must report IsGit() == true")
	}
	if SchemePath.IsGit() || SchemePathEditable.IsGit() {
		t.Error("path schemes must report IsGit() == false")
	}
}

func TestScheme_IsEditable(t *testing.T) {
	if !SchemePathEditable.IsEditable() {
		t.Error("SchemePathEditable.IsEditable() = false, want true")
	}
	for _, s := range []Scheme{SchemeGitHTTPS, SchemeGitSSH, SchemePath} {
		if s.IsEditable() {
			t.Errorf("%v.IsEditable() = true, want false", s)
		}
	}
}
