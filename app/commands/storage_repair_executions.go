package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/persistence"
)

var storageRepairExecutionsDryRun bool

var storageRepairExecutionsCmd = &cobra.Command{
	Use:   "repair-node-executions",
	Short: "Fix node executions hidden behind a newer cleaned execution",
	Long: `Finds every node whose unreleased (failed or produced) execution is
outranked by a newer cleaned execution of the same node. A session load reads
the cleaned one as current, so 'plect up' cannot retry the node and fails with
"a concurrent writer already created execution". Builds that persisted a
failed setup attempt at sequence 0 left that shape behind.

The repair raises each such execution's sequence above every sequence its
session records, which makes it the node's current execution again. It
changes only that one column: no execution, output or history is deleted.

Stop every plect process against the data directory first: the backup is a
raw file copy of storage.db (plus its -wal/-shm siblings) taken after the
read-only scan but before the database is opened for writing or migrated, and
it is only a reliable snapshot when nothing else is
writing. A store with nothing to repair is left untouched and gets no backup.
--dry-run lists the affected executions without writing anything. The data
directory is the global --data-home.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		dbPath := persistence.DefaultPath()
		// A missing storage.db means the data directory is wrong; failing
		// here keeps EnsureCurrent from minting an empty one.
		if _, err := os.Stat(dbPath); err != nil {
			return fmt.Errorf("storage.db not readable at %s: %w", dbPath, err)
		}

		found, err := persistence.ShadowedNodeExecutions(cmd.Context(), dbPath)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			fmt.Fprintln(out, "nothing to repair")
			return nil
		}
		if storageRepairExecutionsDryRun {
			for _, e := range found {
				fmt.Fprintf(out, "would repair %s/%s: execution %s (%s) at sequence %d\n", e.SessionName, e.NodeID, e.ExecutionID, e.Status, e.Sequence)
			}
			fmt.Fprintf(out, "dry run: %d execution(s) would be repaired; nothing written\n", len(found))
			return nil
		}

		backupPath, err := persistence.BackupDatabaseFiles(dbPath)
		if err != nil {
			return fmt.Errorf("back up %s: %w", dbPath, err)
		}
		db, err := persistence.EnsureCurrent(cmd.Context(), dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		repaired, err := db.RepairShadowedNodeExecutions(cmd.Context())
		if err != nil {
			return err
		}
		for _, e := range repaired {
			fmt.Fprintf(out, "repaired %s/%s: execution %s sequence %d -> %d\n", e.SessionName, e.NodeID, e.ExecutionID, e.Sequence, e.NewSequence)
		}
		fmt.Fprintf(out, "repaired %d execution(s); backup: %s\n", len(repaired), backupPath)
		return nil
	},
}

func init() {
	storageRepairExecutionsCmd.Flags().BoolVar(&storageRepairExecutionsDryRun, "dry-run", false, "List the affected executions without taking a backup or writing anything")
	storageCmd.AddCommand(storageRepairExecutionsCmd)
}
