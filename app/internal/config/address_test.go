package config

import (
	"strings"
	"testing"
)

func TestAddressHint(t *testing.T) {
	addresses := []string{"official.claude.runtime", "official.codex.exec_runtime", "acme.b.runtime", "local_effect"}
	tests := []struct {
		name string
		ref  string
		want []string
	}{
		{
			name: "a bare id one plugin declares names that address",
			ref:  "exec_runtime",
			want: []string{"official.codex.exec_runtime"},
		},
		{
			name: "a bare id two plugins declare names both rather than guessing",
			ref:  "runtime",
			want: []string{"official.claude.runtime", "acme.b.runtime"},
		},
		{
			name: "a reference that already carries an address gets no hint",
			ref:  "official.claude.runtime",
			want: nil,
		},
		{
			name: "an id nothing declares gets no hint",
			ref:  "nowhere",
			want: nil,
		},
		{
			name: "a user-owned id gets no hint: it is already the address",
			ref:  "local_effect",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddressHint(addresses, tt.ref)
			if len(tt.want) == 0 {
				if got != "" {
					t.Fatalf("AddressHint(%q) = %q, want no hint", tt.ref, got)
				}
				return
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("AddressHint(%q) = %q, want it to name %q", tt.ref, got, want)
				}
			}
		})
	}
}
