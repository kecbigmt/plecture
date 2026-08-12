package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/service"
	"github.com/cradel-dev/cradel/app/internal/state"
)

var (
	destroyForce        bool
	destroyDeleteBranch bool
)

var destroyCmd = &cobra.Command{
	Use:   "destroy <resource-id|session>",
	Short: "Tear down a session via task cleanup",
	Long: `Tear down a session in four ordered steps:

  1. run-scoped cleanup (auto-down) — skipped if no run-scoped task is
     currently in "produced" status
  2. session-scoped cleanup
  3. worktree removal
  4. state entry deletion

Default policy is fail-fast: the first cleanup error aborts the remaining
steps and the state entry is kept so you can inspect and retry. Use
--force to switch to best-effort teardown (see flag description).

If the session has child sessions, destroy fails before any teardown step
runs: deleting it would orphan them (sennit up never re-adopts an orphan). Use
--force to destroy anyway and orphan the children, or reset with
` + "`sennit down`" + ` + ` + "`sennit up`" + ` instead, which keeps the state entry and its
children intact.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.Destroy(cfg, store, service.DestroyParams{
			Identifier:   args[0],
			Force:        destroyForce,
			DeleteBranch: destroyDeleteBranch,
			Observer:     newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Destroyed session: %s\n", result.SessionName)
		for _, w := range result.CleanupWarnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		if result.WorktreeWarning != "" {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", result.WorktreeWarning)
		}
		if result.RemovedWorktree {
			fmt.Fprintln(os.Stderr, "Removed worktree")
		}
		return nil
	},
}

func init() {
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Demote cleanup errors to warnings (recorded in CleanupWarnings) so teardown continues through worktree + state deletion; also passes --force to git worktree remove so a dirty worktree can be removed; and proceeds when the session has child sessions, orphaning them (reported as a warning) instead of aborting")
	destroyCmd.Flags().BoolVarP(&destroyDeleteBranch, "delete-branch", "b", false, "Also delete the local branch")
	rootCmd.AddCommand(destroyCmd)
}
