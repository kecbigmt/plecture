package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/workspace"
)

var (
	workspaceAddRepo            string
	workspaceAddBranch          string
	workspaceAddBaseBranch      string
	workspaceAddSession         string
	workspaceAddFallbackRefspec string

	workspaceRemovePath         string
	workspaceRemoveBranch       string
	workspaceRemoveForce        bool
	workspaceRemoveDeleteBranch bool
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Acquire and release git worktrees for a provider setup script",
	Long: `Acquire and release git worktrees under the configured worktrees root.

These subcommands are the seam a provider's setup/cleanup scripts call: they
own the worktree path convention, the primary-checkout lookup, and the
idempotent reuse of an existing worktree or branch, while the provider owns
everything about the resource it is acquiring the worktree for (which
repository, which branch, which fallback refspec).`,
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Create (or reuse) a worktree and print its details as JSON",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if workspaceAddRepo == "" || workspaceAddBranch == "" {
			return fmt.Errorf("--repo and --branch are required")
		}
		cfg := config.Load()
		mgr := workspace.NewManager(cfg.WorktreesRoot)
		info, err := mgr.Add(cmd.Context(), workspace.AddParams{
			Repo:            workspaceAddRepo,
			Branch:          workspaceAddBranch,
			BaseBranch:      workspaceAddBaseBranch,
			SessionName:     workspaceAddSession,
			FallbackRefspec: workspaceAddFallbackRefspec,
		})
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, info.JSON())
		return nil
	},
}

var workspaceRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a worktree by path, optionally reclaiming its branch",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if workspaceRemovePath == "" {
			return fmt.Errorf("--path is required")
		}
		if workspaceRemoveDeleteBranch && workspaceRemoveBranch == "" {
			return fmt.Errorf("--delete-branch requires --branch")
		}
		cfg := config.Load()
		mgr := workspace.NewManager(cfg.WorktreesRoot)
		container := workspace.ContainerDir(workspaceRemovePath)
		// The worktree being removed is excluded so it is never chosen as the
		// repository the removal is issued against.
		gitDir, err := mgr.FindGitDir(container, workspaceRemovePath)
		if err != nil {
			return err
		}
		return mgr.RemoveByPath(cmd.Context(), workspaceRemovePath, gitDir, workspaceRemoveBranch, workspaceRemoveForce, workspaceRemoveDeleteBranch)
	},
}

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceAddRepo, "repo", "", "Repository path relative to the worktrees root")
	workspaceAddCmd.Flags().StringVar(&workspaceAddBranch, "branch", "", "Branch to check out in the worktree")
	workspaceAddCmd.Flags().StringVar(&workspaceAddBaseBranch, "base-branch", "", "Branch the target branch is created from (default: the target branch itself)")
	workspaceAddCmd.Flags().StringVar(&workspaceAddSession, "session", "", "Session name to stamp on the printed details")
	workspaceAddCmd.Flags().StringVar(&workspaceAddFallbackRefspec, "fallback-refspec", "", "Refspec fetched when fetching the base branch by name fails")

	workspaceRemoveCmd.Flags().StringVar(&workspaceRemovePath, "path", "", "Worktree path to remove")
	workspaceRemoveCmd.Flags().StringVar(&workspaceRemoveBranch, "branch", "", "Branch checked out in the worktree")
	workspaceRemoveCmd.Flags().BoolVar(&workspaceRemoveForce, "force", false, "Remove the worktree even when it has uncommitted changes")
	workspaceRemoveCmd.Flags().BoolVar(&workspaceRemoveDeleteBranch, "delete-branch", false, "Delete the branch after removing the worktree")

	workspaceCmd.AddCommand(workspaceAddCmd)
	workspaceCmd.AddCommand(workspaceRemoveCmd)
	rootCmd.AddCommand(workspaceCmd)
}
