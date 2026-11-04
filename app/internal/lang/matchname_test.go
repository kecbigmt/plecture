package lang

import (
	"strings"
	"testing"
)

func TestMatchNameFailurePaths(t *testing.T) {
	tests := []struct {
		name    string
		match   string
		value   map[string]any
		id      string
		wantErr string
	}{
		{
			name:    "a match that does not compile",
			match:   `^(`,
			value:   map[string]any{"from": "match.n"},
			id:      "x",
			wantErr: "match",
		},
		{
			name:    "a name reading a capture the match does not declare",
			match:   `^x/(?P<n>\d+)$`,
			value:   map[string]any{"from": "match.other"},
			id:      "x/1",
			wantErr: "name",
		},
		{
			name:    "a name that evaluates to the empty string",
			match:   `^x/(?P<n>\d*)$`,
			value:   map[string]any{"from": "match.n"},
			id:      "x/",
			wantErr: "empty",
		},
		{
			name:    "a name that evaluates to whitespace only",
			match:   `^x/(?P<n>\s+)$`,
			value:   map[string]any{"from": "match.n"},
			id:      "x/  ",
			wantErr: "empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, err := ParseValue(tc.value, ClassData, Position{File: "f.toml", Path: "r.name"})
			if err != nil {
				t.Fatal(err)
			}
			got, matched, err := MatchName(tc.match, name, tc.id)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.wantErr)
			}
			if got != "" || matched {
				t.Errorf("a failed resolution returned (%q, %v), want no name", got, matched)
			}
		})
	}
}
