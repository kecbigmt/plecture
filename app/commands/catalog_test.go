package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestWriteCatalogList_FormatsEntries(t *testing.T) {
	var buf bytes.Buffer
	err := writeCatalogList(&buf, []service.CatalogListEntry{
		{Alias: "official", Source: "git+https://x", Subdir: "plugins", ResolvedRevision: "abc123", Status: "ok", EnabledPlugins: []string{"github"}},
		{Alias: "local", Source: "path+editable:///x", Status: "ok", EnabledPlugins: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"official", "plugins", "abc123", "github", "local", "(none, path source)", "(none, source root)"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

func TestOrNone(t *testing.T) {
	if got := orNone(""); got != "(none, path source)" {
		t.Errorf("orNone(\"\") = %q", got)
	}
	if got := orNone("abc123"); got != "abc123" {
		t.Errorf("orNone(\"abc123\") = %q", got)
	}
}

func TestOrNoneSubdir(t *testing.T) {
	if got := orNoneSubdir(""); got != "(none, source root)" {
		t.Errorf("orNoneSubdir(\"\") = %q", got)
	}
	if got := orNoneSubdir("plugins"); got != "plugins" {
		t.Errorf("orNoneSubdir(\"plugins\") = %q", got)
	}
}

func TestConfirm_ParsesYesVariants(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"\n", false},
		{"whatever\n", false},
	}
	for _, tt := range tests {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(tt.input))
		var out bytes.Buffer
		cmd.SetOut(&out)
		if got := confirm(cmd, "Register?"); got != tt.want {
			t.Errorf("confirm(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
