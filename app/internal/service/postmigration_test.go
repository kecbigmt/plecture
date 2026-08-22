package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/state"
)

// postMigrationStateJSON is a state directory in the shape the one-time
// identity migration leaves behind: sessions carry only the canonical
// resource id and the create-time alias, with no legacy provider-shaped
// identity keys at all.
const postMigrationStateJSON = `{
  "version": 7,
  "sessions": {
    "acme/widgets-1+claude": {
      "session_name": "acme/widgets-1+claude",
      "resource_id": "https://example.test/acme/widgets/items/1",
      "alias": "https://example.test/acme/widgets/items/1",
      "branch": "item/1+claude",
      "workspace_dir_path": "/tmp/workdirs/acme-widgets-1-claude",
      "workflow": "coding",
      "tasks": {
        "@workflow": {
          "scope": "session",
          "status": "produced",
          "outputs": {"workspace_dir": "/tmp/workdirs/acme-widgets-1-claude"}
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    },
    "standalone": {
      "session_name": "standalone",
      "resource_id": "standalone",
      "alias": "standalone",
      "workspace_dir_path": "/tmp/workdirs/standalone",
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    }
  }
}`

// writePostMigrationState materializes the post-migration fixture in a temp
// state directory and returns a store rooted there.
func writePostMigrationState(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(postMigrationStateJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return state.NewStore(dir)
}

// TestPostMigrationState_LoadsIdentityFields pins that a state file with no
// legacy identity keys loads with its canonical identity intact.
func TestPostMigrationState_LoadsIdentityFields(t *testing.T) {
	store := writePostMigrationState(t)

	s := store.Get("acme/widgets-1+claude")
	if s == nil {
		t.Fatal("session not loaded from post-migration state")
	}
	if s.ResourceID != "https://example.test/acme/widgets/items/1" {
		t.Errorf("ResourceID = %q", s.ResourceID)
	}
	if s.Alias != "https://example.test/acme/widgets/items/1" {
		t.Errorf("Alias = %q", s.Alias)
	}
	if s.Branch != "item/1+claude" {
		t.Errorf("Branch = %q", s.Branch)
	}
	if s.WorkspaceDirPath != "/tmp/workdirs/acme-widgets-1-claude" {
		t.Errorf("WorkspaceDirPath = %q", s.WorkspaceDirPath)
	}
}

// TestPostMigrationState_ResolveSession pins every lookup order a
// post-migration session must still support: by name, by alias, and by
// canonical resource id.
func TestPostMigrationState_ResolveSession(t *testing.T) {
	store := writePostMigrationState(t)

	tests := []struct {
		name       string
		identifier string
		want       string
	}{
		{"by session name", "acme/widgets-1+claude", "acme/widgets-1+claude"},
		{"by alias", "https://example.test/acme/widgets/items/1", "acme/widgets-1+claude"},
		{"identity session by name", "standalone", "standalone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, sess, err := ResolveSession(nil, store, tt.identifier)
			if err != nil {
				t.Fatalf("ResolveSession(%q): %v", tt.identifier, err)
			}
			if name != tt.want || sess == nil || sess.Name != tt.want {
				t.Errorf("resolved to %q, want %q", name, tt.want)
			}
		})
	}
}

// TestPostMigrationState_UnknownIdentifier pins the failure path: an
// identifier with no state entry and no resolver must report not-found
// rather than inventing a session.
func TestPostMigrationState_UnknownIdentifier(t *testing.T) {
	store := writePostMigrationState(t)

	if _, _, err := ResolveSession(nil, store, "no-such-session"); err == nil {
		t.Fatal("expected an error for an identifier with no state entry")
	} else if svcErr, ok := err.(*Error); !ok || svcErr.Code != ErrSessionNotFound {
		t.Errorf("error = %v, want ErrSessionNotFound", err)
	}
}

// TestPostMigrationState_WorkspaceDirFromSession pins that the working directory
// lookup reads the session's recorded workspace directory, with no
// resource-shape
// derivation involved.
func TestPostMigrationState_WorkspaceDirFromSession(t *testing.T) {
	store := writePostMigrationState(t)

	got, err := WorkspaceDir(nil, store, "acme/widgets-1+claude")
	if err != nil {
		t.Fatalf("WorkspaceDir: %v", err)
	}
	if got != "/tmp/workdirs/acme-widgets-1-claude" {
		t.Errorf("WorkspaceDir = %q", got)
	}
}

// TestPostMigrationState_ResolverDispatchOverPostMigrationState is the
// end-to-end check that the current code operates on a state directory in
// post-migration shape: a session recorded there is found by the same
// resolver dispatch a fresh create would use, so a migrated session is
// reused rather than duplicated.
func TestPostMigrationState_ResolverDispatchOverPostMigrationState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	migrated := `{
  "version": 7,
  "sessions": {
    "acme/widgets-42+gh": {
      "session_name": "acme/widgets-42+gh",
      "resource_id": "https://github.com/acme/widgets/issues/42",
      "alias": "https://github.com/acme/widgets/issues/42",
      "workflow": "gh",
      "branch": "issue/42+gh",
      "workspace_dir_path": "/tmp/workdirs/issue-42-gh",
      "tasks": {
        "@workflow": {
          "scope": "session",
          "status": "produced",
          "outputs": {"workspace_dir": "/tmp/workdirs/issue-42-gh"}
        }
      },
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z"
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(migrated), 0o644); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(dir)

	cfg := writeWorkflowFixture(t, t.TempDir(), "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeSetupWorkflow(t, cfg, "gh", providerEchoingOutputs("gh", `{"workspace_dir":"/tmp/x"}`))

	disp, matched, err := dispatchResource(cfg, "", "https://github.com/acme/widgets/issues/42")
	if err != nil || !matched {
		t.Fatalf("dispatch: matched=%v err=%v", matched, err)
	}
	name := disp.Name + "+gh"
	if store.Get(name) == nil {
		t.Fatalf("resolver-derived name %q does not address the migrated session", name)
	}

	// Every identity lookup a command performs must land on the same session.
	for _, identifier := range []string{name, "https://github.com/acme/widgets/issues/42"} {
		got, _, err := ResolveSession(cfg, store, identifier)
		if err != nil {
			t.Fatalf("ResolveSession(%q): %v", identifier, err)
		}
		if got != name {
			t.Errorf("ResolveSession(%q) = %q, want %q", identifier, got, name)
		}
	}
}
