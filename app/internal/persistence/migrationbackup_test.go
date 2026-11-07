package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const migrationC = `-- +goose Up
CREATE TABLE c (id INTEGER PRIMARY KEY);
`

var (
	fixtureV1 = map[string]string{"00001_a.sql": migrationA}
	fixtureV2 = map[string]string{"00001_a.sql": migrationA, "00002_b.sql": migrationBOK}
	fixtureV3 = map[string]string{"00001_a.sql": migrationA, "00002_b.sql": migrationBOK, "00003_c.sql": migrationC}
)

// seedFixtureStore creates a store at the fixture's version holding one
// row in table a, so a later restore can be checked for the row's survival.
func seedFixtureStore(t *testing.T, path string, files map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, err := ensureCurrent(ctx, path, migrationFixture(files), false, false)
	if err != nil {
		t.Fatalf("seed ensureCurrent: %v", err)
	}
	defer db.Close()
	if _, err := db.write.ExecContext(ctx, "INSERT INTO a (id) VALUES (42)"); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// migrateFixture runs Migrate directly, rather than through ensureCurrent,
// so the fixture schema never meets the real sidecar importer.
func migrateFixture(t *testing.T, path string, files map[string]string) (string, error) {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.migrations = migrationFixture(files)
	var diag bytes.Buffer
	db.diagnostics = &diag
	err = db.Migrate(context.Background())
	return diag.String(), err
}

func ledgerVersionOf(t *testing.T, path string) int64 {
	t.Helper()
	raw, err := sql.Open("sqlite3", readOnlyURI(path))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer raw.Close()
	var v int64
	if err := raw.QueryRow("SELECT max(version_id) FROM goose_db_version").Scan(&v); err != nil {
		t.Fatalf("read ledger of %s: %v", path, err)
	}
	return v
}

func rowCountOf(t *testing.T, path string) int {
	t.Helper()
	raw, err := sql.Open("sqlite3", readOnlyURI(path))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer raw.Close()
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM a").Scan(&n); err != nil {
		t.Fatalf("count rows of %s: %v", path, err)
	}
	return n
}

func backupNames(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read backups: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrate_BacksUpTheStoreAtItsFromVersionBeforeMigrating(t *testing.T) {
	for _, mode := range []string{"WAL", "DELETE", "TRUNCATE"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(JournalModeEnvVar, mode)
			testBacksUpTheStoreAtItsFromVersion(t)
		})
	}
}

func testBacksUpTheStoreAtItsFromVersion(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)

	diag, err := migrateFixture(t, path, fixtureV2)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	names := backupNames(t, dir)
	if len(names) != 1 || !strings.HasSuffix(names[0], "-v1") {
		t.Fatalf("backups = %v, want exactly one named <stamp>-v1", names)
	}
	backupDB := filepath.Join(dir, "backups", names[0], "storage.db")
	if got := ledgerVersionOf(t, backupDB); got != 1 {
		t.Errorf("backup ledger version = %d, want 1", got)
	}
	if got := rowCountOf(t, backupDB); got != 1 {
		t.Errorf("backup rows in a = %d, want the seeded row", got)
	}
	if got := ledgerVersionOf(t, path); got != 2 {
		t.Errorf("live ledger version = %d, want 2", got)
	}
	for _, suffix := range journalSuffixes {
		if _, err := os.Stat(backupDB + suffix); !os.IsNotExist(err) {
			t.Errorf("backup has %s (err %v), want one self-contained file", filepath.Base(backupDB+suffix), err)
		}
	}
	for _, want := range []string{"schema 1", "to 2", filepath.Join(dir, "backups", names[0])} {
		if !strings.Contains(diag, want) {
			t.Errorf("diagnostics = %q, want it to mention %q", diag, want)
		}
	}
}

func TestEnsureCurrent_CreatingAStoreTakesNoBackup(t *testing.T) {
	dir := t.TempDir()
	seedFixtureStore(t, PathIn(dir), fixtureV2)

	if names := backupNames(t, dir); len(names) != 0 {
		t.Errorf("backups = %v, want none for a store created at schema 0", names)
	}
}

func TestMigrate_BackupFailureAppliesNoMigration(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	writeFile(t, filepath.Join(dir, "backups"), "a file where the backups directory belongs")

	_, err := migrateFixture(t, path, fixtureV2)
	if err == nil {
		t.Fatal("Migrate succeeded although its backup could not be written")
	}
	if !strings.Contains(err.Error(), "backup") || !strings.Contains(err.Error(), "no migration applied") {
		t.Errorf("error = %q, want it to name the backup failure and that no migration was applied", err)
	}
	if got := ledgerVersionOf(t, path); got != 1 {
		t.Errorf("ledger version = %d, want 1 (unchanged)", got)
	}
	marker, merr := readMarker(newAccessGate(path).markerPath)
	if merr != nil {
		t.Fatalf("readMarker: %v", merr)
	}
	if marker == nil || marker.Stage != "failed" || marker.Error == "" {
		t.Errorf("marker = %+v, want stage failed with the backup error recorded", marker)
	}
}

func TestMigrate_FailedMigrationRecordsItsBackupInTheMarker(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)

	_, err := migrateFixture(t, path, map[string]string{"00001_a.sql": migrationA, "00002_b.sql": migrationBBroken})
	if err == nil {
		t.Fatal("Migrate with a broken migration unexpectedly succeeded")
	}
	marker, merr := readMarker(newAccessGate(path).markerPath)
	if merr != nil || marker == nil {
		t.Fatalf("readMarker = %+v, %v; want failure evidence", marker, merr)
	}
	if marker.Backup == "" {
		t.Fatal("marker.Backup is empty, want the pre-migration backup path")
	}
	if got := ledgerVersionOf(t, filepath.Join(marker.Backup, "storage.db")); got != 1 {
		t.Errorf("recorded backup ledger version = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), marker.Backup) {
		t.Errorf("error = %q, want it to name the backup %s", err, marker.Backup)
	}
}

func TestMigrate_BackupIncludesRetiredSidecars(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	writeFile(t, filepath.Join(dir, "pending_delivery.json"), `{"subscribe":{}}`)
	writeFile(t, filepath.Join(dir, "events", "s", "chain_attempts.json"), `{}`)
	writeFile(t, filepath.Join(dir, "events", "gone", "tombstone.json"), `{}`)

	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	names := backupNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("backups = %v, want one", names)
	}
	for _, rel := range []string{"pending_delivery.json", "events/s/chain_attempts.json", "events/gone/tombstone.json"} {
		if _, err := os.Stat(filepath.Join(dir, "backups", names[0], rel)); err != nil {
			t.Errorf("backup lacks %s: %v", rel, err)
		}
	}
}

func TestMigrate_PrunesTheOldestBackupsBeyondRetention(t *testing.T) {
	t.Setenv(BackupRetainEnvVar, "2")
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	for _, name := range []string{"20200101T000000Z-v1", "20210101T000000Z-v1", ".tmp-20220101T000000Z-v1"} {
		writeFile(t, filepath.Join(dir, "backups", name, "storage.db"), "")
	}

	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	names := backupNames(t, dir)
	if len(names) != 2 || names[0] != "20210101T000000Z-v1" || !strings.HasSuffix(names[1], "-v1") || names[1] <= names[0] {
		t.Errorf("backups = %v, want the newer existing backup plus the new one, oldest and leftover temp pruned", names)
	}
}

func TestMigrate_RetainZeroTakesNoBackupAndPrunesNothing(t *testing.T) {
	t.Setenv(BackupRetainEnvVar, "0")
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	writeFile(t, filepath.Join(dir, "backups", "20200101T000000Z-v1", "storage.db"), "")

	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if names := backupNames(t, dir); len(names) != 1 || names[0] != "20200101T000000Z-v1" {
		t.Errorf("backups = %v, want only the pre-existing one", names)
	}
	if got := ledgerVersionOf(t, path); got != 2 {
		t.Errorf("ledger version = %d, want 2", got)
	}
}

func TestEnsureCurrent_RefusesAnInvalidBackupRetention(t *testing.T) {
	for _, value := range []string{"-1", "three", "1.5"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(BackupRetainEnvVar, value)
			db, err := ensureCurrent(context.Background(), testDBPath(t), migrationFixture(fixtureV1), false, false)
			if err == nil {
				db.Close()
				t.Fatalf("ensureCurrent accepted %s=%q", BackupRetainEnvVar, value)
			}
			if !strings.Contains(err.Error(), BackupRetainEnvVar) {
				t.Errorf("error = %q, want it to name %s", err, BackupRetainEnvVar)
			}
		})
	}
}

func TestListBackups_NewestFirstWithLedgerVersionAndSize(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate to 2: %v", err)
	}
	if _, err := migrateFixture(t, path, fixtureV3); err != nil {
		t.Fatalf("Migrate to 3: %v", err)
	}
	writeFile(t, filepath.Join(dir, "backups", ".tmp-20990101T000000Z-v9", "storage.db"), "")

	backups, err := ListBackups(context.Background(), path)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("backups = %+v, want the two completed ones", backups)
	}
	if backups[0].Version != 2 || backups[1].Version != 1 {
		t.Errorf("versions = %d, %d; want newest (2) first, then 1", backups[0].Version, backups[1].Version)
	}
	for _, b := range backups {
		if b.Size <= 0 {
			t.Errorf("backup %s size = %d, want positive", b.Name, b.Size)
		}
	}
}

func TestRestore_PutsTheBackupBackAndDisplacesTheCurrentFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	writeFile(t, filepath.Join(dir, "pending_delivery.json"), `{"subscribe":{}}`)
	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "pending_delivery.json")); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		writeFile(t, path+suffix, "stale journal of the newer store")
	}
	name := backupNames(t, dir)[0]

	displaced, err := restore(ctx, path, name, migrationFixture(fixtureV2))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if !strings.HasPrefix(displaced, filepath.Join(dir, "restore-displaced")+string(filepath.Separator)) {
		t.Errorf("displaced = %s, want a directory under %s", displaced, filepath.Join(dir, "restore-displaced"))
	}
	for _, name := range []string{"storage.db", "storage.db-wal", "storage.db-shm", "storage.db-journal"} {
		if _, err := os.Stat(filepath.Join(displaced, name)); err != nil {
			t.Errorf("displaced lacks %s: %v", name, err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Errorf("%s still beside the restored store (err %v); a stale journal must never meet it", filepath.Base(path+suffix), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pending_delivery.json")); err != nil {
		t.Errorf("restored data home lacks the backed-up sidecar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", name, "storage.db")); err != nil {
		t.Errorf("backup consumed by restore, want it kept restorable: %v", err)
	}

	old, err := ensureCurrent(ctx, path, migrationFixture(fixtureV1), false, false)
	if err != nil {
		t.Fatalf("the previous binary's ensureCurrent after restore: %v", err)
	}
	defer old.Close()
	if v, err := old.Version(ctx); err != nil || v != 1 {
		t.Errorf("restored version = %d, %v; want 1", v, err)
	}
	if got := rowCountOf(t, path); got != 1 {
		t.Errorf("restored rows in a = %d, want the seeded row", got)
	}
}

func TestRestore_RefusedWhileAHandleIsOpen(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	idle, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idle.Close()

	_, err = restore(context.Background(), path, backupNames(t, dir)[0], migrationFixture(fixtureV2))
	if err == nil {
		t.Fatal("restore succeeded while an idle handle held the store open")
	}
	if !strings.Contains(err.Error(), "open") {
		t.Errorf("error = %q, want it to say a process has the store open", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("storage.db changed although restore refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-displaced")); !os.IsNotExist(err) {
		t.Errorf("restore-displaced exists after a refusal (err %v)", err)
	}
}

func TestRestore_RefusedWhileAMigrationHoldsTheCoordinationLock(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	unlock, err := flockPath(newAccessGate(path).coordinationLockPath, syscall.LOCK_EX)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	if _, err := restore(context.Background(), path, backupNames(t, dir)[0], migrationFixture(fixtureV2)); err == nil {
		t.Fatal("restore succeeded while the coordination lock was held")
	}
}

func TestRestore_RefusesABackupNewerThanThisBinary(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV2)
	if _, err := migrateFixture(t, path, fixtureV3); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err := restore(context.Background(), path, backupNames(t, dir)[0], migrationFixture(fixtureV1))
	if err == nil {
		t.Fatal("restore accepted a schema-2 backup on a binary that supports only 1")
	}
	for _, want := range []string{"2", "1", "newer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if got := ledgerVersionOf(t, path); got != 3 {
		t.Errorf("live ledger version = %d, want 3 (untouched)", got)
	}
}

func TestRestore_RefusesNamesThatAreNotACompletedBackup(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	writeFile(t, filepath.Join(dir, "backups", ".tmp-20200101T000000Z-v1", "storage.db"), "")
	if err := os.MkdirAll(filepath.Join(dir, "backups", "20200101T000000Z-v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "backups", "manual-copy", "storage.db"), "")

	for _, name := range []string{"", "missing", "../backups", "x/y", ".tmp-20200101T000000Z-v1", "20200101T000000Z-v1", "manual-copy", "20200101T000000Z-v1/../manual-copy"} {
		t.Run(name, func(t *testing.T) {
			if _, err := restore(context.Background(), path, name, migrationFixture(fixtureV1)); err == nil {
				t.Fatalf("restore accepted %q", name)
			}
		})
	}
	if got := ledgerVersionOf(t, path); got != 1 {
		t.Errorf("ledger version = %d, want 1 (untouched)", got)
	}
}

func TestBackupBeforeMigrate_SameSecondBackupsKeepCreationOrder(t *testing.T) {
	for _, retain := range []int{1, 3} {
		t.Run(strconv.Itoa(retain), func(t *testing.T) {
			t.Setenv(BackupRetainEnvVar, strconv.Itoa(retain))
			fixed := time.Date(2026, 11, 8, 1, 30, 0, 0, time.UTC)
			t.Cleanup(func(fn func() time.Time) func() { return func() { clock = fn } }(clock))
			clock = func() time.Time { return fixed }
			dir := t.TempDir()
			path := PathIn(dir)
			seedFixtureStore(t, path, fixtureV1)
			db, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			db.migrations = migrationFixture(fixtureV2)
			db.diagnostics = io.Discard

			var created []string
			for i := 1; i <= 6; i++ {
				backup, err := db.backupBeforeMigrate(context.Background(), 1, 2)
				if err != nil {
					t.Fatalf("backup %d: %v", i, err)
				}
				created = append(created, filepath.Base(backup))

				listed, err := ListBackups(context.Background(), path)
				if err != nil {
					t.Fatalf("ListBackups after backup %d: %v", i, err)
				}
				var got []string
				for _, b := range listed {
					got = append(got, b.Name)
				}
				var want []string
				for k := len(created) - 1; k >= 0 && len(want) < retain; k-- {
					want = append(want, created[k])
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Fatalf("after backup %d: listed %v, want the newest %d newest-first: %v", i, got, retain, want)
				}
			}
		})
	}
}

func TestRestore_ChecksForLiveUsersBeforeReadingTheBackup(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	unlock, err := flockPath(newAccessGate(path).coordinationLockPath, syscall.LOCK_EX)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	_, err = restore(context.Background(), path, "20200101T000000Z-v1", migrationFixture(fixtureV1))
	if err == nil || !strings.Contains(err.Error(), "migrating") {
		t.Errorf("error = %v, want the live-user refusal: a migration holding the lock may be pruning the backup", err)
	}
}

func TestStageBackup_RefusesABackupWithoutAStore(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backup")
	writeFile(t, filepath.Join(src, "pending_delivery.json"), `{}`)

	if err := stageBackup(src, filepath.Join(dir, "staging")); err == nil {
		t.Fatal("stageBackup staged a backup that has no storage.db")
	}
}

func TestRestore_WorksInADataHomeWhosePathNeedsURIEscaping(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data #1 %20home")
	path := PathIn(dir)
	seedFixtureStore(t, path, fixtureV1)
	if _, err := migrateFixture(t, path, fixtureV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	backups, err := ListBackups(ctx, path)
	if err != nil || len(backups) != 1 || backups[0].Err != nil || backups[0].Version != 1 {
		t.Fatalf("ListBackups = %+v, %v; want one readable schema-1 backup", backups, err)
	}
	if _, err := restore(ctx, path, backups[0].Name, migrationFixture(fixtureV2)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := ledgerVersionOf(t, path); got != 1 {
		t.Errorf("restored ledger version = %d, want 1", got)
	}
}
