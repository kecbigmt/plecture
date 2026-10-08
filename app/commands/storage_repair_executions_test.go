package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/confighome"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/persistence"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// scratchShadowedStore points the default data home at a scratch HOME and
// seeds a storage.db holding one node whose failed setup attempt sits below
// an older cleaned execution (the stored shape the repair exists for). It
// returns the database path.
func scratchShadowedStore(t *testing.T, shadowed bool) string {
	t.Helper()
	t.Cleanup(func() { storageRepairExecutionsDryRun = false })
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv(confighome.EnvVar, "")
	t.Setenv(confighome.XDGEnvVar, "")

	dbPath := persistence.PathIn(filepath.Join(fakeHome, ".local", "share", "plect"))
	ctx := context.Background()
	db, err := persistence.EnsureCurrent(ctx, dbPath)
	if err != nil {
		t.Fatalf("seed storage.db: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	put := func(st *contract.TaskState) {
		t.Helper()
		if err := db.PutSession(ctx, &domain.Session{Name: "s1", CreatedAt: now, UpdatedAt: now, Nodes: map[string]*contract.TaskState{"a": st}}); err != nil {
			t.Fatalf("PutSession: %v", err)
		}
	}
	put(&contract.TaskState{Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 17})
	if !shadowed {
		return dbPath
	}
	session, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	released := session.Nodes["a"]
	released.Status = contract.TaskStatusCleaned
	put(released)
	put(&contract.TaskState{Scope: contract.TaskScopeRun, Status: contract.TaskStatusFailed, NewExecution: true, Error: "boom"})
	return dbPath
}

func backupsOf(t *testing.T, dbPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(dbPath + ".backup-*")
	if err != nil {
		t.Fatal(err)
	}
	var dbs []string
	for _, m := range matches {
		if !strings.HasSuffix(m, "-wal") && !strings.HasSuffix(m, "-shm") {
			dbs = append(dbs, m)
		}
	}
	return dbs
}

func TestStorageRepairNodeExecutions_DryRunReportsWithoutBackupOrWrite(t *testing.T) {
	dbPath := scratchShadowedStore(t, true)

	out, err := execRoot(t, "storage", "repair-node-executions", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "s1/a") || !strings.Contains(out, "dry run") {
		t.Errorf("output = %q, want the affected s1/a node named and a dry-run note", out)
	}
	if got := backupsOf(t, dbPath); len(got) != 0 {
		t.Errorf("dry run wrote backups %v", got)
	}
	left, err := persistence.ShadowedNodeExecutions(context.Background(), dbPath)
	if err != nil || len(left) != 1 {
		t.Fatalf("after dry run shadowed = %+v, %v; want the damage untouched", left, err)
	}
}

func TestStorageRepairNodeExecutions_BacksUpThenRepairs(t *testing.T) {
	dbPath := scratchShadowedStore(t, true)

	out, err := execRoot(t, "storage", "repair-node-executions")
	if err != nil {
		t.Fatalf("repair: %v; output:\n%s", err, out)
	}
	backups := backupsOf(t, dbPath)
	if len(backups) != 1 || !strings.Contains(out, backups[0]) {
		t.Fatalf("backups = %v, output = %q; want exactly one backup named in the output", backups, out)
	}
	if kept, err := persistence.ShadowedNodeExecutions(context.Background(), backups[0]); err != nil || len(kept) != 1 {
		t.Fatalf("backup holds shadowed = %+v, %v; want the pre-repair damage preserved", kept, err)
	}
	if left, err := persistence.ShadowedNodeExecutions(context.Background(), dbPath); err != nil || len(left) != 0 {
		t.Fatalf("after repair shadowed = %+v, %v; want none", left, err)
	}

	db, err := persistence.EnsureCurrent(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	session, err := db.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if node := session.Nodes["a"]; node.Status != contract.TaskStatusFailed || node.Error != "boom" {
		t.Fatalf("node after repair = %+v, want the failed attempt current", node)
	}
}

func TestStorageRepairNodeExecutions_HealthyStoreIsLeftWithoutABackup(t *testing.T) {
	dbPath := scratchShadowedStore(t, false)

	out, err := execRoot(t, "storage", "repair-node-executions")
	if err != nil {
		t.Fatalf("repair: %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to repair") {
		t.Errorf("output = %q, want it to say nothing to repair", out)
	}
	if got := backupsOf(t, dbPath); len(got) != 0 {
		t.Errorf("a store with nothing to repair got backups %v", got)
	}
}

func TestStorageRepairNodeExecutions_MissingDatabaseFailsWithoutCreatingOne(t *testing.T) {
	t.Cleanup(func() { storageRepairExecutionsDryRun = false })
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv(confighome.EnvVar, "")
	t.Setenv(confighome.XDGEnvVar, "")

	if _, err := execRoot(t, "storage", "repair-node-executions"); err == nil {
		t.Fatal("repair against a missing storage.db unexpectedly succeeded")
	}
	dbPath := persistence.PathIn(filepath.Join(fakeHome, ".local", "share", "plect"))
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("stat %s = %v, want not-exist", dbPath, statErr)
	}
}
