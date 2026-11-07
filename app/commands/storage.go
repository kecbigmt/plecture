package commands

import (
	"fmt"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/legacyimport"
	"github.com/kecbigmt/plecture/app/internal/persistence"
)

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Inspect and maintain plect's local SQLite database",
}

var storageMigrateAllowDevBuild bool

var storageMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply any pending schema migrations to the local database",
	Long: `Runs the same migration runner every plect command, plect serve, and
plect-web already run automatically at startup, explicitly. Useful to apply a
pending migration ahead of time, or to retry after a previously failed
migration once its cause is resolved.

A development build (one the release pipeline did not version-stamp) refuses
a database it did not create rather than migrating it; pass --allow-dev-build
to migrate deliberately anyway. A release build always migrates, with or
without the flag.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := persistence.DefaultPath()
		ensure := persistence.EnsureCurrent
		if storageMigrateAllowDevBuild {
			ensure = persistence.EnsureCurrentAllowDevBuild
		}
		db, err := ensure(cmd.Context(), path)
		if err != nil {
			return err
		}
		defer db.Close()

		version, err := db.Version(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is at schema version %d\n", path, version)
		return nil
	},
}

var (
	storageImportFrom     string
	storageImportDataHome string
	storageImportDryRun   bool
	storageImportTmpDir   string
)

var storageImportCmd = &cobra.Command{
	Use:   "import",
	Short: "One-time import of a stopped-writer legacy data directory into SQLite",
	Long: `Reads a legacy plect data directory (state.json, the events/ tree) from
--from, builds and validates a temporary SQLite database, and — unless
--dry-run — atomically promotes it to storage.db and writes the legacy
rejection marker over state.json in the target data directory, defaulting to
$PLECT_DATA_HOME, else $XDG_DATA_HOME/plect.

Every writer against --from must already be stopped, and --from should be a
backup copy, not the live data directory: this command validates that no
lock file it reads is still held, but it does not stop anything itself. See
docs/migrations/ for the full cutover procedure.

The database this command builds is written under --tmp-dir (default: the
OS temporary directory) while it is being built and validated, then copied
into the target data directory only once finished — so a target data
directory on a network filesystem never sees the many small writes building
it produces, only the single final copy.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataHome := storageImportDataHome
		if dataHome == "" {
			dataHome = defaultDataHome()
		}
		report, err := legacyimport.Run(cmd.Context(), legacyimport.Options{
			SourceDir: storageImportFrom,
			DestDir:   dataHome,
			DryRun:    storageImportDryRun,
			TmpDir:    storageImportTmpDir,
		})
		if report != nil {
			fmt.Fprintln(cmd.OutOrStdout(), report.String())
		}
		return err
	},
}

var (
	storageRepairFrom     string
	storageRepairDataHome string
	storageRepairDryRun   bool
)

var storageRepairCmd = &cobra.Command{
	Use:   "repair-imported-sessions",
	Short: "One-time fix for a host already imported before the importer stopped materializing events/-only sessions",
	Long: `A build older than the fix that made 'plect storage import' skip an
events/-only legacy session entirely left every such session as a ghost row
on a host that already ran that older import. This command deletes every
session (and its events) that the already-promoted storage.db at
--data-home holds, that --from's state.json does not name, and that
--from's events/ tree still has a directory for -- the exact shape that
older importer left behind. A session --from's backup names nowhere at all
(for example, one created after the backup was taken) is reported as
not-in-backup and left untouched.

Stop every plect process against --data-home first (same prerequisite as
'plect storage import'): this command's raw file-copy backup and its delete
pass both assume nothing else is writing to storage.db concurrently, or
neither is a reliable snapshot of what existed.

It takes a dated backup of storage.db (plus its -wal/-shm siblings, if any)
before deleting anything, unless --dry-run. This command is a stopgap: it can
be removed once every host has run it, or once v0.3.0 ships, whichever comes
first.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataHome := storageRepairDataHome
		if dataHome == "" {
			dataHome = defaultDataHome()
		}
		report, err := legacyimport.RepairImportedSessions(cmd.Context(), legacyimport.RepairOptions{
			SourceDir: storageRepairFrom,
			DestDir:   dataHome,
			DryRun:    storageRepairDryRun,
		})
		if report != nil {
			fmt.Fprintln(cmd.OutOrStdout(), report.String())
		}
		return err
	},
}

var storageBackupsCmd = &cobra.Command{
	Use:   "backups",
	Short: "List the backups taken before each schema migration",
	Long: `Lists, newest first, the backups plect takes in <data home>/backups/
before it applies a schema migration to an existing store: each backup's
name, the schema version recorded in its own migration ledger, and its size.

$` + persistence.BackupRetainEnvVar + ` sets how many backups are kept (default 3);
0 stops taking them.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		backups, err := persistence.ListBackups(cmd.Context(), persistence.DefaultPath())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(backups) == 0 {
			fmt.Fprintf(out, "no backups in %s\n", filepath.Join(defaultDataHome(), "backups"))
			return nil
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSCHEMA\tSIZE")
		for _, b := range backups {
			schema := strconv.FormatInt(b.Version, 10)
			if b.Err != nil {
				schema = "unreadable: " + b.Err.Error()
			}
			fmt.Fprintf(w, "%s\t%s\t%d\n", b.Name, schema, b.Size)
		}
		return w.Flush()
	},
}

var storageRestoreCmd = &cobra.Command{
	Use:   "restore <backup>",
	Short: "Replace the store with a backup taken before a schema migration",
	Long: `Copies the named backup (as 'plect storage backups' lists it) back into
the data home. The files it replaces -- storage.db, its -wal/-shm/-journal
siblings, and any retired sidecars -- are moved to
<data home>/restore-displaced/<timestamp>/, and the backup itself is kept.

Stop every plect process using the data home first (plect serve, plect-web,
and any command): this command refuses while any of them has the store open.
It also refuses a backup whose schema is newer than this binary supports.

Restoring is a rollback only for the moment right after an upgrade, before
anything has been written to the new schema: whatever was written since the
backup is not carried over. Run it with either binary, then start the
earlier one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		displaced, err := persistence.Restore(cmd.Context(), persistence.DefaultPath(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "restored %s into %s; the replaced files are in %s\n", args[0], defaultDataHome(), displaced)
		return nil
	},
}

// defaultDataHome is the directory persistence.DefaultPath's storage.db
// lives in, derived rather than duplicated so the two never disagree about
// data-home resolution.
func defaultDataHome() string {
	return filepath.Dir(persistence.DefaultPath())
}

func init() {
	storageMigrateCmd.Flags().BoolVar(&storageMigrateAllowDevBuild, "allow-dev-build", false,
		"Let a development build forward-migrate a database it did not create (a release build always may)")
	storageCmd.AddCommand(storageMigrateCmd)

	storageImportCmd.Flags().StringVar(&storageImportFrom, "from", "", "Legacy data directory to import (a stopped-writer backup, not the live directory)")
	storageImportCmd.Flags().StringVar(&storageImportDataHome, "data-home", "", "Target plect data directory (default: $PLECT_DATA_HOME, else $XDG_DATA_HOME/plect)")
	storageImportCmd.Flags().BoolVar(&storageImportDryRun, "dry-run", false, "Validate and report without promoting a database or writing the rejection marker")
	storageImportCmd.Flags().StringVar(&storageImportTmpDir, "tmp-dir", "", "Directory to build the scratch database under before copying it into --data-home (default: the OS temporary directory)")
	_ = storageImportCmd.MarkFlagRequired("from")
	storageCmd.AddCommand(storageImportCmd)

	storageRepairCmd.Flags().StringVar(&storageRepairFrom, "from", "", "The same legacy backup directory --from used for the original import")
	storageRepairCmd.Flags().StringVar(&storageRepairDataHome, "data-home", "", "The plect data directory holding the storage.db to repair (default: $XDG_DATA_HOME/plect)")
	storageRepairCmd.Flags().BoolVar(&storageRepairDryRun, "dry-run", false, "Report what would be deleted without writing anything")
	_ = storageRepairCmd.MarkFlagRequired("from")
	storageCmd.AddCommand(storageRepairCmd)

	storageCmd.AddCommand(storageBackupsCmd)
	storageCmd.AddCommand(storageRestoreCmd)

	rootCmd.AddCommand(storageCmd)
}
