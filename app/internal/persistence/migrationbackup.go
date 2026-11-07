package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

// BackupRetainEnvVar is how many pre-migration backups to keep (default
// defaultBackupRetain); 0 disables them. It is an environment variable, not
// a config.toml key, because the store is opened and migrated before any
// configuration is loaded.
const BackupRetainEnvVar = "PLECT_STORAGE_BACKUP_RETAIN"

const defaultBackupRetain = 3

var clock = time.Now

const (
	backupsDirName   = "backups"
	displacedDirName = "restore-displaced"
	tmpPrefix        = ".tmp-"
	stampLayout      = "20060102T150405Z"
)

// journalSuffixes are the files SQLite keeps beside the database. A restore
// always moves them aside: replaying a newer store's WAL or hot journal onto
// the restored file would corrupt it.
var journalSuffixes = []string{"-wal", "-shm", "-journal"}

func backupRetain() (int, error) {
	v := os.Getenv(BackupRetainEnvVar)
	if v == "" {
		return defaultBackupRetain, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("persistence: %s=%q is not a non-negative integer (0 disables pre-migration backups)", BackupRetainEnvVar, v)
	}
	return n, nil
}

func (db *DB) backupBeforeMigrate(ctx context.Context, from, to int64) (string, error) {
	retain, err := backupRetain()
	if err != nil {
		return "", err
	}
	if retain == 0 || from == 0 {
		return "", nil
	}
	dataDir := filepath.Dir(db.path)
	backupsDir := filepath.Join(dataDir, backupsDirName)
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", backupsDir, err)
	}
	complete, err := completedBackups(backupsDir)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-v%d", nextStamp(complete).Format(stampLayout), from)
	tmp := filepath.Join(backupsDir, tmpPrefix+name)
	final := filepath.Join(backupsDir, name)
	if err := db.writeBackup(ctx, dataDir, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("move backup into place: %w", err)
	}
	if err := syncDir(backupsDir); err != nil {
		return "", err
	}
	fmt.Fprintf(db.diagnostics, "plect: storage: backed up schema %d to %s before migrating to %d\n", from, final, to)

	// A prune failure leaves an extra backup behind, not a store at risk,
	// so it warns instead of refusing the migration the new backup protects.
	if err := pruneBackups(backupsDir, retain); err != nil {
		fmt.Fprintf(db.diagnostics, "plect: storage: warning: pruning old backups in %s: %v\n", backupsDir, err)
	}
	return final, nil
}

func (db *DB) writeBackup(ctx context.Context, dataDir, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clear leftover %s: %w", dest, err)
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	if err := db.onlineBackup(ctx, PathIn(dest)); err != nil {
		return err
	}
	for _, name := range retiredSidecarNames {
		if err := copyTree(filepath.Join(dataDir, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	return syncDir(dest)
}

// onlineBackup uses SQLite's backup API rather than copying the file: the
// copy is page-exact and self-contained whatever the journal mode, with no
// -wal/-shm to carry along and no checkpoint that another process's open
// read snapshot could block.
func (db *DB) onlineBackup(ctx context.Context, dest string) error {
	destDB, err := sql.Open("sqlite3", fileURI(dest, ""))
	if err != nil {
		return fmt.Errorf("open backup database: %w", err)
	}
	defer destDB.Close()
	destConn, err := destDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open backup database: %w", err)
	}
	defer destConn.Close()
	srcConn, err := db.write.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open source connection for backup: %w", err)
	}
	defer srcConn.Close()

	return destConn.Raw(func(d any) error {
		return srcConn.Raw(func(s any) error {
			dc, ok := d.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("backup destination is %T, not a SQLite connection", d)
			}
			sc, ok := s.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("backup source is %T, not a SQLite connection", s)
			}
			b, err := dc.Backup("main", sc, "main")
			if err != nil {
				return fmt.Errorf("start backup: %w", err)
			}
			done, stepErr := b.Step(-1)
			if finishErr := b.Finish(); stepErr == nil {
				stepErr = finishErr
			}
			if stepErr != nil {
				return fmt.Errorf("copy database: %w", stepErr)
			}
			if !done {
				return errors.New("copy database: backup did not complete")
			}
			return nil
		})
	})
}

// nextStamp is the clock's second, or one second past the newest backup when
// the clock has not passed it (a same-second retry, or a clock set back), so
// a backup's name always sorts after every backup taken before it: pruning
// and listing order backups by name.
func nextStamp(complete []backupName) time.Time {
	stamp := clock().UTC().Truncate(time.Second)
	if n := len(complete); n > 0 && !stamp.After(complete[n-1].stamp) {
		stamp = complete[n-1].stamp.Add(time.Second)
	}
	return stamp
}

type backupName struct {
	name  string
	stamp time.Time
}

// completedBackups returns the backups in backupsDir oldest first. Entries
// whose name is not <stamp>-v<version> are left alone: plect did not write
// them, so it neither lists nor prunes them.
func completedBackups(backupsDir string) ([]backupName, error) {
	entries, err := os.ReadDir(backupsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var backups []backupName
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if stamp, ok := parseBackupName(e.Name()); ok {
			backups = append(backups, backupName{name: e.Name(), stamp: stamp})
		}
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].stamp.Before(backups[j].stamp) })
	return backups, nil
}

func parseBackupName(name string) (time.Time, bool) {
	stampText, version, ok := strings.Cut(name, "-v")
	if !ok {
		return time.Time{}, false
	}
	if _, err := strconv.ParseInt(version, 10, 64); err != nil {
		return time.Time{}, false
	}
	stamp, err := time.Parse(stampLayout, stampText)
	return stamp, err == nil
}

// pruneBackups also deletes leftover temporary directories: the caller holds
// migration exclusion, so none of them is a backup still being written.
func pruneBackups(backupsDir string, retain int) error {
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), tmpPrefix) {
			errs = append(errs, os.RemoveAll(filepath.Join(backupsDir, e.Name())))
		}
	}
	complete, err := completedBackups(backupsDir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for i := 0; i < len(complete)-retain; i++ {
		errs = append(errs, os.RemoveAll(filepath.Join(backupsDir, complete[i].name)))
	}
	return errors.Join(errs...)
}

// Backup's Version comes from the backup's own goose ledger, never from its
// name, which is only a label for people.
type Backup struct {
	Name    string
	Version int64
	Size    int64
	Err     error
}

func ListBackups(ctx context.Context, dbPath string) ([]Backup, error) {
	backupsDir := filepath.Join(filepath.Dir(dbPath), backupsDirName)
	complete, err := completedBackups(backupsDir)
	if err != nil {
		return nil, fmt.Errorf("persistence: list backups: %w", err)
	}
	backups := make([]Backup, 0, len(complete))
	for i := len(complete) - 1; i >= 0; i-- {
		dir := filepath.Join(backupsDir, complete[i].name)
		b := Backup{Name: complete[i].name}
		b.Version, _, b.Err = backupLedger(ctx, PathIn(dir), migrationsSourceFS())
		b.Size, err = treeSize(dir)
		if err != nil {
			return nil, fmt.Errorf("persistence: size of backup %s: %w", b.Name, err)
		}
		backups = append(backups, b)
	}
	return backups, nil
}

// readOnlyURI escapes the path because SQLite reads '#', '?', and '%' in a
// URI as delimiters. immutable=1 takes no lock and creates no journal
// beside the file, so reading a backup never writes to it.
func readOnlyURI(path string) string {
	return fileURI(path, "mode=ro&immutable=1")
}

func fileURI(path, query string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String()
}

func backupLedger(ctx context.Context, path string, migrations fs.FS) (current, target int64, err error) {
	if _, err := os.Stat(path); err != nil {
		return 0, 0, err
	}
	raw, err := sql.Open("sqlite3", readOnlyURI(path))
	if err != nil {
		return 0, 0, err
	}
	defer raw.Close()
	// goose would try, and retry, to create a missing ledger table.
	var tables int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'").Scan(&tables); err != nil {
		return 0, 0, err
	}
	if tables == 0 {
		return 0, 0, errors.New("no migration ledger")
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, raw, migrations)
	if err != nil {
		return 0, 0, fmt.Errorf("create migration provider: %w", err)
	}
	current, err = provider.GetDBVersion(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, source := range provider.ListSources() {
		if source.Version > target {
			target = source.Version
		}
	}
	return current, target, nil
}

// Restore refuses while any process holds the store open, because files
// replaced under an open handle would take that process's later writes with
// them. It returns the directory the replaced files were moved into.
func Restore(ctx context.Context, dbPath, name string) (string, error) {
	return restore(ctx, dbPath, name, migrationsSourceFS())
}

func restore(ctx context.Context, dbPath, name string, migrations fs.FS) (string, error) {
	if _, ok := parseBackupName(name); !ok || name != filepath.Base(name) {
		return "", fmt.Errorf("persistence: %q is not a backup name; `plect storage backups` lists them", name)
	}
	// Locked before the backup is read: a migration's prune, which runs under
	// the coordination lock, could otherwise delete it after validation.
	unlock, err := lockForRestore(newAccessGate(dbPath))
	if err != nil {
		return "", err
	}
	defer unlock()

	dataDir := filepath.Dir(dbPath)
	src := filepath.Join(dataDir, backupsDirName, name)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return "", fmt.Errorf("persistence: no backup named %q in %s", name, filepath.Dir(src))
	}
	version, target, err := backupLedger(ctx, PathIn(src), migrations)
	if err != nil {
		return "", fmt.Errorf("persistence: backup %s is not restorable: read its ledger: %w", name, err)
	}
	if version > target {
		return "", fmt.Errorf("persistence: backup %s is at schema %d, newer than this binary supports (max %d); restore it with a newer plect binary", name, version, target)
	}

	stamp := clock().UTC().Format(stampLayout)
	staging := filepath.Join(dataDir, ".restore-"+stamp)
	if err := stageBackup(src, staging); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	displacedRoot := filepath.Join(dataDir, displacedDirName)
	if err := os.MkdirAll(displacedRoot, 0o700); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("persistence: create %s: %w", displacedRoot, err)
	}
	displacedName, err := freeName(displacedRoot, stamp)
	if err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	displaced := filepath.Join(displacedRoot, displacedName)
	if err := os.Mkdir(displaced, 0o700); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("persistence: create %s: %w", displaced, err)
	}

	displace := []string{fileName, filepath.Base(newAccessGate(dbPath).markerPath)}
	for _, suffix := range journalSuffixes {
		displace = append(displace, fileName+suffix)
	}
	displace = append(displace, retiredSidecarNames...)
	for _, entry := range displace {
		if err := moveIfExists(filepath.Join(dataDir, entry), filepath.Join(displaced, entry)); err != nil {
			return "", fmt.Errorf("persistence: restore stopped while moving the current files aside (moved so far: %s; backup staged at %s): %w", displaced, staging, err)
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return "", fmt.Errorf("persistence: restore stopped after moving the current files to %s (backup staged at %s): %w", displaced, staging, err)
	}
	for _, e := range staged {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dataDir, e.Name())); err != nil {
			return "", fmt.Errorf("persistence: restore stopped after moving the current files to %s (backup staged at %s): %w", displaced, staging, err)
		}
	}
	if err := os.Remove(staging); err != nil {
		return "", fmt.Errorf("persistence: remove restore staging directory: %w", err)
	}
	if err := syncDir(dataDir); err != nil {
		return "", err
	}
	return displaced, nil
}

// lockForRestore takes every lock a store user can hold, without waiting:
// the coordination lock (a migration or an Open in progress), the handle
// lock (any open handle, idle or not), and the access lock (a query).
func lockForRestore(gate *accessGate) (func(), error) {
	var unlocks []func()
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	for _, path := range []string{gate.coordinationLockPath, gate.handleLockPath, gate.accessLockPath} {
		unlock, ok, err := tryFlockPath(path, syscall.LOCK_EX)
		if err != nil {
			release()
			return nil, fmt.Errorf("persistence: lock %s: %w", filepath.Base(path), err)
		}
		if !ok {
			release()
			return nil, fmt.Errorf("persistence: refusing to restore: a plect process has %s open or is migrating it; stop every plect process using this data home (serve, plect-web, commands) and retry", fileName)
		}
		unlocks = append(unlocks, unlock)
	}
	return release, nil
}

// stageBackup copies rather than moves, so the backup stays restorable.
func stageBackup(src, staging string) error {
	if err := os.Mkdir(staging, 0o700); err != nil {
		return fmt.Errorf("persistence: create restore staging directory: %w", err)
	}
	if err := copyFile(PathIn(src), PathIn(staging), 0o600); err != nil {
		return fmt.Errorf("persistence: stage backup: %w", err)
	}
	for _, name := range retiredSidecarNames {
		if err := copyTree(filepath.Join(src, name), filepath.Join(staging, name)); err != nil {
			return fmt.Errorf("persistence: stage backup: %w", err)
		}
	}
	return syncDir(staging)
}

func freeName(dir, base string) (string, error) {
	name := base
	for i := 2; ; i++ {
		_, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func moveIfExists(src, dst string) error {
	if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func copyTree(src, dst string) error {
	if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("copy %s: not a regular file or directory", path)
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return fmt.Errorf("sync %s: %w", dst, err)
	}
	return out.Close()
}

func treeSize(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
