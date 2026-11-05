package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var (
	destroyForce         bool
	destroyCleanupInputs []string
)

var destroyCmd = &cobra.Command{
	Use:   "destroy <resource-id|session>",
	Short: "Tear down a session via task cleanup",
	Long: `Tear down a session in three ordered steps:

  1. run-scoped cleanup (auto-down) — skipped if no run-scoped task is
     currently in "produced" status
  2. session-scoped cleanup
  3. state entry deletion

Default policy is fail-fast: the first cleanup error aborts the remaining
steps and the state entry is kept so you can inspect and retry. Use
--force to switch to best-effort teardown (see flag description).

--force means you accept losing unsaved local changes: it is the cleanup
actions' "force" value, and an effect whose release step refuses on unsaved
local changes may discard them to complete the release instead of leaving them
on disk.

--input passes a key=value cleanup intent (repeatable). Each key must be
declared in the cleanup input schema of an effect being released, and is handed
only to the effects that declare it; core does not interpret it. Consult an
effect's own docs for which keys it reads.

If the session has child sessions, destroy fails before any teardown step
runs: deleting it would orphan them (plect up never re-adopts an orphan). Use
--force to destroy anyway and orphan the children, or reset with
` + "`plect down`" + ` + ` + "`plect up`" + ` instead, which keeps the state entry and its
children intact.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cleanupInputs, err := parseKeyValues(destroyCleanupInputs)
		if err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		result, err := service.Destroy(cfg, store, service.DestroyParams{
			Identifier:    args[0],
			Force:         destroyForce,
			CleanupInputs: cleanupInputs,
			Observer:      newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Destroyed session: %s\n", result.SessionName)
		for _, w := range result.CleanupWarnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		return nil
	},
}

func init() {
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Demote cleanup errors to warnings (recorded in CleanupWarnings) so teardown continues through every effect's cleanup and state deletion; also forwards the force intent to cleanup actions, where it may discard unsaved local changes in a directory an effect created to complete the release, and proceeds when the session has child sessions, orphaning them (reported as a warning) instead of aborting")
	destroyCmd.Flags().StringArrayVar(&destroyCleanupInputs, "input", nil, "Cleanup input key=value, validated against the cleanup input schemas of the effects being released (repeatable)")
	rootCmd.AddCommand(destroyCmd)
}
