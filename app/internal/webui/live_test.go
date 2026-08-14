package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/state"
)

func TestNewLiveServiceRejectsStateVersionMismatch(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	stateDir := filepath.Join(dataHome, "plect")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{
  "version": 5,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "workdir_path": "/tmp/workdir"
    }
  }
}`)
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, original, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewLiveService()
	if err == nil {
		t.Fatal("NewLiveService() over a mismatched state version must fail")
	}
	for _, part := range []string{"state schema version mismatch", "got 5", "want 6", "go run ./plugins/legacy-migration/cmd/legacy-migration"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), part)
		}
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("mismatched state file was rewritten: got %q, want unchanged %q", data, original)
	}
}

func TestLiveServiceListSurfacesStateVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	writeMismatchedState(t, filepath.Join(dir, "state.json"))

	rec := get(t, newLiveService(nil, state.NewStore(dir)), "/sessions")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "No sessions") {
		t.Fatalf("mismatched state rendered empty-state placeholder: %q", body)
	}
	if !strings.Contains(body, "state schema version mismatch") {
		t.Fatalf("body = %q, want state schema mismatch", body)
	}
}

func writeMismatchedState(t *testing.T, path string) []byte {
	t.Helper()
	original := []byte(`{
  "version": 5,
  "sessions": {
    "org/repo-1": {
      "session_name": "org/repo-1",
      "workdir_path": "/tmp/workdir"
    }
  }
}`)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	return original
}
