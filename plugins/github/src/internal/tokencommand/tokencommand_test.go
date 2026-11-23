package tokencommand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token helper")
	for _, tc := range []struct {
		name, script, want string
		failure            bool
	}{
		{"success", "printf 'secret-token\\n'", "secret-token", false},
		{"crlf", "printf 'secret-token\\r\\n'", "secret-token", false},
		{"failure", "printf 'secret-token\\n'; echo secret-token >&2; exit 7", "exit status 7", true},
		{"empty", "exit 0", "exit status 0", true},
		{"multiline", "printf 'secret-token\\nother\\n'", "exit status 0", true},
		{"oversized", "printf 'secret-token'; head -c 8192 /dev/zero | tr '\\000' x", "exit status 0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			got, err := Run([]string{path})
			if tc.failure {
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-token") {
					t.Fatalf("result length %d, error %v", len(got), err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("result %q, error %v", got, err)
			}
		})
	}
}
