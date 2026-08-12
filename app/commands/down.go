package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/app/internal/state"
)

var (
	downRemove       bool
	downForce        bool
	downDeleteBranch bool
)

var downCmd = &cobra.Command{
	Use:   "down <resource-id|session>",
	Short: "Run run-scoped cleanup for a session",
	Long: `Run cleanup commands for all run-scoped tasks in reverse dependency order.
session-scoped tasks are preserved.

Pass --rm to remove the session after cleanup. The removal path runs full
teardown and deletes the state entry; --force switches that path to
best-effort teardown, matching the cleanup-error and child-session override
policy documented on the flag.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		if !downRemove && (downForce || downDeleteBranch) {
			return fmt.Errorf("--force and --delete-branch require --rm")
		}
		if downRemove {
			result, err := service.Destroy(cfg, store, service.DestroyParams{
				Identifier:   args[0],
				Force:        downForce,
				DeleteBranch: downDeleteBranch,
				Observer:     newTaskObserver(cfg),
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Removed session: %s\n", result.SessionName)
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
		}
		result, err := service.Down(cfg, store, service.DownParams{
			Identifier: args[0],
			Observer:   newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Session: %s\n", result.SessionName)
		return nil
	},
}

func init() {
	downCmd.Flags().BoolVar(&downRemove, "rm", false, "Remove the session after cleanup, including session-scoped tasks and state")
	downCmd.Flags().BoolVarP(&downForce, "force", "f", false, "With --rm, demote cleanup errors to warnings so teardown continues, and proceed when child sessions would be orphaned")
	downCmd.Flags().BoolVarP(&downDeleteBranch, "delete-branch", "b", false, "With --rm, also delete the local branch")
	rootCmd.AddCommand(downCmd)
}
