package service

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// A workspace provider's resolver fails with the provider's id, so the
// declaration an author must fix is named, whichever way the resolution broke.
func TestTryResolveNameFailurePathsNameTheProvider(t *testing.T) {
	tests := []struct {
		name  string
		match string
		value map[string]any
		id    string
	}{
		{name: "a match that does not compile", match: `^(`, value: map[string]any{"from": "match.n"}, id: "x"},
		{name: "a name reading an undeclared capture", match: `^x/(?P<n>\d+)$`, value: map[string]any{"from": "match.other"}, id: "x/1"},
		{name: "a name that evaluates to the empty string", match: `^x/(?P<n>\d*)$`, value: map[string]any{"from": "match.n"}, id: "x/"},
		{name: "a name that evaluates to whitespace only", match: `^x/(?P<n>\s+)$`, value: map[string]any{"from": "match.n"}, id: "x/  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, err := lang.ParseValue(tc.value, lang.ClassData, lang.Position{File: "p.toml", Path: "prov.name"})
			if err != nil {
				t.Fatal(err)
			}
			prov := config.WorkspaceProviderConfig{ID: "prov", Match: tc.match, Name: name}
			got, matched, err := tryResolveName(prov, tc.id)
			if err == nil || !strings.Contains(err.Error(), `"prov"`) {
				t.Fatalf("error = %v, want one naming provider \"prov\"", err)
			}
			if got != "" || matched {
				t.Errorf("a failed resolution returned (%q, %v), want no name", got, matched)
			}
		})
	}
}
