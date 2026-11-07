package commands

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/persistence"
)

func TestStorageRestore_RollsBackAStoreThisBinaryRefusesAsTooNew(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PLECT_DATA_HOME", "")
	t.Cleanup(func() { storageMigrateAllowDevBuild = false })
	seedRealBehindSchemaDatabase(t, fakeHome)
	dataDir := filepath.Join(fakeHome, ".local", "share", "plect")

	if out, err := execRoot(t, "storage", "migrate", "--allow-dev-build"); err != nil {
		t.Fatalf("storage migrate: %v; output:\n%s", err, out)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v, %v; want the one taken before migrating", entries, err)
	}
	name := entries[0].Name()

	raw, err := sql.Open("sqlite3", persistence.PathIn(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999999999999, 1)"); err != nil {
		t.Fatalf("simulate a store written by a newer binary: %v", err)
	}
	raw.Close()
	if out, err := execRoot(t, "status"); err == nil || !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("status against the newer store: err = %v, want the newer-than-supported refusal; output:\n%s", err, out)
	}

	listed, err := execRoot(t, "storage", "backups")
	if err != nil {
		t.Fatalf("storage backups: %v; output:\n%s", err, listed)
	}
	if !strings.Contains(listed, name) {
		t.Errorf("storage backups output = %q, want it to list %s", listed, name)
	}

	restored, err := execRoot(t, "storage", "restore", name)
	if err != nil {
		t.Fatalf("storage restore: %v; output:\n%s", err, restored)
	}
	if !strings.Contains(restored, filepath.Join(dataDir, "restore-displaced")) {
		t.Errorf("storage restore output = %q, want it to name where the replaced files went", restored)
	}

	if out, err := execRoot(t, "storage", "migrate", "--allow-dev-build"); err != nil {
		t.Errorf("storage migrate after restore: %v; output:\n%s", err, out)
	}
}
