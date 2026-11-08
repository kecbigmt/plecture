package persistence

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/pressly/goose/v3"

	appdb "github.com/kecbigmt/plecture/app/db"
)

func migrationsSourceFS() fs.FS {
	sub, err := fs.Sub(appdb.Migrations, "migrations")
	if err != nil {
		// appdb.Migrations is compiled in via a go:embed directive; a
		// missing "migrations" subtree would be a build-time packaging
		// bug, not a runtime condition a caller can recover from.
		panic(fmt.Sprintf("persistence: embedded migrations tree missing: %v", err))
	}
	return sub
}

// Migrate is the migration runner protocol in full — the only path that is
// allowed to change storage.db's schema, whether called directly (as this
// package's own tests do for setup) or from EnsureCurrent: it records
// intent by acquiring the coordination lock exclusively, waits out any
// already-in-flight normal access by then acquiring the access lock
// exclusively, rechecks the ledger under that exclusion (another process
// may have already migrated, or the ledger may already be newer than this
// binary supports), and preserves diagnostic evidence in the marker file
// across a failure. It defers entirely to goose's own applied-version
// ledger rather than tracking a second record of what has run, so it is
// safe to call on every startup: goose.Provider.Up is a no-op once the
// ledger shows every embedded migration already applied.
func (db *DB) Migrate(ctx context.Context) error {
	gate := db.gate

	unlockCoord, err := gate.acquireCoordinationExclusive(ctx)
	if err != nil {
		return err
	}
	defer unlockCoord()

	// Written and fsynced immediately, before accessExclusive below (which
	// can wait out an in-flight normal access for a while): per design,
	// intent is recorded the moment this process commits to migrating —
	// i.e. the moment it holds the coordination lock exclusively — not
	// once it has also finished waiting for the access lock. Otherwise a
	// process that times out waiting behind this coordination hold sees no
	// diagnostic detail, and this process exiting during that wait would
	// leave no evidence at all.
	marker := migrationMarker{
		PID:           os.Getpid(),
		BinaryVersion: binaryVersion(),
		StartedAt:     time.Now().UTC(),
		Stage:         "migrating",
	}
	if err := writeMarker(gate.markerPath, marker); err != nil {
		return err
	}

	unlockAccess, err := gate.accessExclusive()
	if err != nil {
		return err
	}
	defer unlockAccess()

	current, target, err := db.versionLocked(ctx)
	if err != nil {
		return err
	}
	if err := refuseIfNewerThanSupported(current, target); err != nil {
		if removeErr := removeMarker(gate.markerPath); removeErr != nil {
			return fmt.Errorf("%w (also failed to remove the marker recorded on intent: %v)", err, removeErr)
		}
		return err
	}
	if current == target {
		// Another process already migrated between this one recording
		// intent and reaching exclusion; nothing to do, so the marker this
		// process speculatively wrote is removed rather than left behind.
		return removeMarker(gate.markerPath)
	}
	if err := db.refuseLayersWithoutSetupTime(ctx, current, target); err != nil {
		if removeErr := removeMarker(gate.markerPath); removeErr != nil {
			return fmt.Errorf("%w (also failed to remove the marker recorded on intent: %v)", err, removeErr)
		}
		return err
	}
	if err := db.refuseUnreleasedWorkflowNodes(ctx, current, target); err != nil {
		if removeErr := removeMarker(gate.markerPath); removeErr != nil {
			return fmt.Errorf("%w (also failed to remove the marker recorded on intent: %v)", err, removeErr)
		}
		return err
	}

	backup, backupErr := db.backupBeforeMigrate(ctx, current, target)
	if backupErr != nil {
		err := fmt.Errorf("persistence: pre-migration backup failed, no migration applied: %w", backupErr)
		marker.Stage = "failed"
		marker.Error = err.Error()
		if writeErr := writeMarker(gate.markerPath, marker); writeErr != nil {
			return fmt.Errorf("%w (also failed to record failure evidence: %v)", err, writeErr)
		}
		return err
	}
	if backup != "" {
		marker.Backup = backup
		if err := writeMarker(gate.markerPath, marker); err != nil {
			return err
		}
	}

	if migrateErr := db.migrateLocked(ctx); migrateErr != nil {
		marker.Stage = "failed"
		marker.Error = migrateErr.Error()
		if writeErr := writeMarker(gate.markerPath, marker); writeErr != nil {
			return fmt.Errorf("%w (also failed to record failure evidence: %v)", migrateErr, writeErr)
		}
		if backup != "" {
			return fmt.Errorf("persistence: migration failed, failure evidence preserved at %s, pre-migration backup at %s: %w", gate.markerPath, backup, migrateErr)
		}
		return fmt.Errorf("persistence: migration failed, pre-migration state and failure evidence preserved at %s: %w", gate.markerPath, migrateErr)
	}

	return removeMarker(gate.markerPath)
}

func removeMarker(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("persistence: remove migration marker: %w", err)
	}
	return nil
}

const (
	firstReleasedVersion          = 20260907002408
	nodeExecutionsVersion         = 20260907085032
	workdirDirectoryVersion       = 20261106000000
	tightenNullableColumnsVersion = 20261108120000
)

const workdirDirectoryRefusal = "destroy live sessions that still hold a workspace provider with the previous release, then upgrade; see docs/migrations/resource-and-workspace-effects-migration.md"

// The workdir-directory migration's own guard fires only after goose has
// committed every earlier pending migration, leaving a store the previous
// release refuses as too new. A ledger below the first release is left to
// that guard: no released binary wrote one.
func (db *DB) refuseUnreleasedWorkflowNodes(ctx context.Context, current, target int64) error {
	if current < firstReleasedVersion || current >= workdirDirectoryVersion || target < workdirDirectoryVersion {
		return nil
	}
	nodes := "node_executions"
	if current < nodeExecutionsVersion {
		nodes = "node_instances"
	}
	var held bool
	if err := db.write.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM `+nodes+` n
		JOIN sessions s ON s.id = n.session_id
		WHERE n.node_id = '@workflow' AND n.status <> 'cleaned' AND s.status <> 'destroyed'
	)`).Scan(&held); err != nil {
		return fmt.Errorf("persistence: check for live sessions holding a workspace provider: %w", err)
	}
	if held {
		return fmt.Errorf("persistence: upgrade refused, no migration applied: %s", workdirDirectoryRefusal)
	}
	return nil
}

// The tightening migration copies a missing layer setup time from the
// layer's owner, and no other value would be true. When the owner has none
// either, the refusal happens here: the migration's own NOT NULL failure
// would only come after every earlier pending migration had committed.
func (db *DB) refuseLayersWithoutSetupTime(ctx context.Context, current, target int64) error {
	if current < firstReleasedVersion || current >= tightenNullableColumnsVersion || target < tightenNullableColumnsVersion {
		return nil
	}
	type layerTable struct{ name, owner, join string }
	tables := []layerTable{
		{"node_execution_layers", "node_executions", "o.id = l.execution_id"},
		{"task_instance_layers", "task_instances", "o.id = l.task_instance_id"},
	}
	if current < nodeExecutionsVersion {
		tables[0] = layerTable{"node_instance_layers", "node_instances", "o.session_id = l.session_id AND o.node_id = l.node_id"}
	}
	if current < workdirDirectoryVersion {
		// The workdir-directory migration deletes these before the tightening runs.
		tables[0].join += " AND o.node_id <> '@workflow'"
	}
	var unfilled []string
	for _, table := range tables {
		var n int
		if err := db.write.QueryRowContext(ctx, `SELECT count(*) FROM `+table.name+` l JOIN `+table.owner+` o ON `+table.join+`
			WHERE l.setup_at IS NULL AND o.setup_at IS NULL`).Scan(&n); err != nil {
			return fmt.Errorf("persistence: count %s rows with no setup time: %w", table.name, err)
		}
		if n > 0 {
			unfilled = append(unfilled, fmt.Sprintf("%s.setup_at is NULL in %d %s whose %s row has no setup_at to copy", table.name, n, plural(n, "row", "rows"), table.owner))
		}
	}
	if len(unfilled) > 0 {
		return fmt.Errorf("persistence: upgrade refused, no migration applied: %s; set or delete those rows with the previous release, then upgrade", strings.Join(unfilled, "; "))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// migrateLocked applies pending migrations without acquiring any lock
// itself; only Migrate calls it, already holding accessExclusive.
func (db *DB) migrateLocked(ctx context.Context) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// version returns the goose ledger's currently applied version and the
// highest version this binary's migration source declares, via
// enterShared like any other normal access.
func (db *DB) version(ctx context.Context) (current, target int64, err error) {
	unlock, err := db.gate.enterShared(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer unlock()

	return db.versionLocked(ctx)
}

// versionLocked is version's body without acquiring the access gate
// itself; Migrate calls it directly while already holding accessExclusive.
func (db *DB) versionLocked(ctx context.Context) (current, target int64, err error) {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.write, db.migrations)
	if err != nil {
		return 0, 0, fmt.Errorf("create migration provider: %w", err)
	}
	current, err = provider.GetDBVersion(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read applied migration version: %w", err)
	}
	for _, source := range provider.ListSources() {
		if source.Version > target {
			target = source.Version
		}
	}
	return current, target, nil
}

// Version returns the goose ledger's currently applied migration version.
func (db *DB) Version(ctx context.Context) (int64, error) {
	current, _, err := db.version(ctx)
	return current, err
}

// enterNormalAccess is what every normal query and write transaction goes
// through — the access gate's own enterShared, plus a recheck that this
// handle's migration source still covers the ledger's current version. The
// gate alone is not enough: a *DB opened while at the latest version can
// stay open (and keep passing the gate's locking) across a later migration
// applied by another process entirely, so each access must also confirm
// its own binary still understands the schema, not just that no migration
// is currently in flight.
func (db *DB) enterNormalAccess(ctx context.Context) (func(), error) {
	unlock, err := db.gate.enterShared(ctx)
	if err != nil {
		return nil, err
	}
	current, target, err := db.versionLocked(ctx)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := refuseIfNewerThanSupported(current, target); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
