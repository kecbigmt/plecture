package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseResourceID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		wantOwner  string
		wantID     string
		wantErr    bool
	}{
		{"well formed", "local-okf://acme/goals/ship-it.md", "acme", "goals/ship-it.md", false},
		{"wrong scheme", "https://example.com/acme/goals/ship-it.md", "", "", true},
		{"missing concept id", "local-okf://acme", "", "", true},
		{"empty owner", "local-okf:///goals/ship-it.md", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, conceptID, err := ParseResourceID(tt.resourceID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseResourceID(%q): want error, got none", tt.resourceID)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseResourceID(%q): unexpected error: %v", tt.resourceID, err)
			}
			if owner != tt.wantOwner || conceptID != tt.wantID {
				t.Fatalf("ParseResourceID(%q) = (%q, %q), want (%q, %q)", tt.resourceID, owner, conceptID, tt.wantOwner, tt.wantID)
			}
		})
	}
}

// fakeRunner replays a canned status response, or fails with a canned
// combined-output error, without shelling out to a real `plect` binary.
type fakeRunner struct {
	output []byte
	err    error
}

func (f fakeRunner) Status(alias string) ([]byte, error) { return f.output, f.err }

func TestResolveOwnerWorkspaceDir(t *testing.T) {
	t.Run("resolved", func(t *testing.T) {
		runner := fakeRunner{output: []byte(`{"runtime":{"workspace_dir_path":"/w","workspace_dir_exists":true}}`)}
		got, resolveErr := ResolveOwnerWorkspaceDir(runner, "acme")
		if resolveErr != nil {
			t.Fatalf("unexpected error: %v", resolveErr)
		}
		if got != "/w" {
			t.Fatalf("got workspace dir %q, want /w", got)
		}
	})

	t.Run("no session is unresolved", func(t *testing.T) {
		runner := fakeRunner{output: []byte("no such session"), err: errors.New("exit 1")}
		_, resolveErr := ResolveOwnerWorkspaceDir(runner, "acme")
		if resolveErr == nil || !resolveErr.Unresolved {
			t.Fatalf("want an Unresolved error, got %#v", resolveErr)
		}
	})

	t.Run("ambiguous alias is a hard error", func(t *testing.T) {
		runner := fakeRunner{output: []byte("owner:acme matches multiple sessions"), err: errors.New("exit 1")}
		_, resolveErr := ResolveOwnerWorkspaceDir(runner, "acme")
		if resolveErr == nil || resolveErr.Unresolved {
			t.Fatalf("want a hard (non-Unresolved) error, got %#v", resolveErr)
		}
	})

	t.Run("unreadable workspace directory is unresolved", func(t *testing.T) {
		runner := fakeRunner{output: []byte(`{"runtime":{"workspace_dir_path":"/w","workspace_dir_exists":false}}`)}
		_, resolveErr := ResolveOwnerWorkspaceDir(runner, "acme")
		if resolveErr == nil || !resolveErr.Unresolved {
			t.Fatalf("want an Unresolved error, got %#v", resolveErr)
		}
	})

	t.Run("unparseable status output is unresolved", func(t *testing.T) {
		runner := fakeRunner{output: []byte("not json")}
		_, resolveErr := ResolveOwnerWorkspaceDir(runner, "acme")
		if resolveErr == nil || !resolveErr.Unresolved {
			t.Fatalf("want an Unresolved error, got %#v", resolveErr)
		}
	})
}

func TestRoot(t *testing.T) {
	t.Run("bundle exists", func(t *testing.T) {
		workspaceDir := t.TempDir()
		bundleDir := filepath.Join(workspaceDir, "knowledge", "bundle")
		if err := os.MkdirAll(bundleDir, 0o755); err != nil {
			t.Fatal(err)
		}
		got, resolveErr := Root(workspaceDir)
		if resolveErr != nil {
			t.Fatalf("unexpected error: %v", resolveErr)
		}
		want, _ := filepath.EvalSymlinks(bundleDir)
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("bundle not bootstrapped is unresolved", func(t *testing.T) {
		workspaceDir := t.TempDir()
		_, resolveErr := Root(workspaceDir)
		if resolveErr == nil || !resolveErr.Unresolved {
			t.Fatalf("want an Unresolved error, got %#v", resolveErr)
		}
	})
}

func TestResolveConceptPath(t *testing.T) {
	newBundle := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "goals"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "goals", "ship-it.md"), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		return real
	}

	t.Run("resolves an existing concept file", func(t *testing.T) {
		root := newBundle(t)
		got, resolveErr := ResolveConceptPath(root, "goals/ship-it.md")
		if resolveErr != nil {
			t.Fatalf("unexpected error: %v", resolveErr)
		}
		want := filepath.Join(root, "goals", "ship-it.md")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("missing file is unresolved", func(t *testing.T) {
		root := newBundle(t)
		_, resolveErr := ResolveConceptPath(root, "goals/missing.md")
		if resolveErr == nil || !resolveErr.Unresolved {
			t.Fatalf("want an Unresolved error, got %#v", resolveErr)
		}
	})

	t.Run("absolute concept id is refused", func(t *testing.T) {
		root := newBundle(t)
		_, resolveErr := ResolveConceptPath(root, "/etc/passwd")
		if resolveErr == nil || resolveErr.Unresolved {
			t.Fatalf("want a hard error, got %#v", resolveErr)
		}
	})

	t.Run("dot-dot traversal escaping the bundle is refused", func(t *testing.T) {
		root := newBundle(t)
		_, resolveErr := ResolveConceptPath(root, "../../etc/passwd")
		if resolveErr == nil || resolveErr.Unresolved {
			t.Fatalf("want a hard error, got %#v", resolveErr)
		}
	})

	t.Run("symlinked ancestor directory escaping the bundle is refused", func(t *testing.T) {
		root := newBundle(t)
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Fatal(err)
		}
		_, resolveErr := ResolveConceptPath(root, "escape/secret.md")
		if resolveErr == nil || resolveErr.Unresolved {
			t.Fatalf("want a hard error, got %#v", resolveErr)
		}
	})

	t.Run("symlinked final component escaping the bundle is refused", func(t *testing.T) {
		root := newBundle(t)
		outside := t.TempDir()
		target := filepath.Join(outside, "secret.md")
		if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "goals", "leak.md")); err != nil {
			t.Fatal(err)
		}
		_, resolveErr := ResolveConceptPath(root, "goals/leak.md")
		if resolveErr == nil || resolveErr.Unresolved {
			t.Fatalf("want a hard error, got %#v", resolveErr)
		}
	})
}
