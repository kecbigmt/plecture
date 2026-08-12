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
	gcExecute      bool
	gcDeleteBranch bool
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Clean up stale sessions",
	Long: `Identify and remove stale sessions (worktrees, runtime sessions, state entries).

By default runs in dry-run mode, showing what would be removed.
Use --execute to actually perform the cleanup.

Completion is declared by task-level done_when predicates. Sessions without
any done_when-bearing tasks have nothing to evaluate and are left alone.

Auto-deleted:
  - Worktree missing: state entry removed; no plan can be built without a
    worktree, so a still-running runtime is left alive for manual cleanup
  - Done + clean worktree with a frozen workflow: removed via non-force
    destroy, so task cleanups (including the runtime) run in order and any
    failure blocks the deletion; without a frozen workflow the same "state
    only, runtime left alive" caveat as worktree-missing applies

Listed for manual attention:
  - Runtime unhealthy but not eligible for auto-delete (not done or dirty worktree)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")

		result, err := service.GC(cfg, store, service.GCParams{
			Execute:      gcExecute,
			DeleteBranch: gcDeleteBranch,
		})
		if err != nil {
			return err
		}

		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}

		if len(result.Entries) == 0 {
			fmt.Fprintln(os.Stderr, "No stale sessions found.")
			return nil
		}

		if !result.Executed {
			fmt.Fprintln(os.Stderr, "Dry-run mode (use --execute to apply):")
			fmt.Fprintln(os.Stderr)
		}

		// Print auto-delete entries
		hasDelete := false
		for _, e := range result.Entries {
			if e.Action != service.GCActionDelete {
				continue
			}
			if !hasDelete {
				fmt.Fprintln(os.Stderr, "Will delete:")
				hasDelete = true
			}
			status := ""
			if result.Executed {
				if e.Deleted {
					status = " [done]"
				} else {
					status = " [partial]"
				}
			}
			fmt.Fprintf(os.Stderr, "  %s%s\n", e.SessionName, status)
			fmt.Fprintf(os.Stderr, "    %s\n", e.Description)
			if e.ResourceID != "" {
				fmt.Fprintf(os.Stderr, "    URL: %s\n", e.ResourceID)
			}
			for _, w := range e.DeleteWarnings {
				fmt.Fprintf(os.Stderr, "    warning: %s\n", w)
			}
		}

		// Print manual attention entries
		hasManual := false
		for _, e := range result.Entries {
			if e.Action != service.GCActionManual {
				continue
			}
			if !hasManual {
				if hasDelete {
					fmt.Fprintln(os.Stderr)
				}
				fmt.Fprintln(os.Stderr, "Needs manual attention:")
				hasManual = true
			}
			fmt.Fprintf(os.Stderr, "  %s\n", e.SessionName)
			fmt.Fprintf(os.Stderr, "    %s\n", e.Description)
			if e.ResourceID != "" {
				fmt.Fprintf(os.Stderr, "    URL: %s\n", e.ResourceID)
			}
		}

		return nil
	},
}

func init() {
	gcCmd.Flags().BoolVar(&gcExecute, "execute", false, "Actually perform cleanup (default is dry-run)")
	gcCmd.Flags().BoolVarP(&gcDeleteBranch, "delete-branch", "b", false, "Also delete local branches for auto-deleted sessions")
	rootCmd.AddCommand(gcCmd)
}
