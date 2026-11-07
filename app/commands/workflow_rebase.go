package commands

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

var workflowRebaseAll bool
var workflowRebaseWorkflow string
var workflowRebaseDryRun bool
var workflowRebaseWithRestart bool

var workflowRebaseCmd = &cobra.Command{
	Use:   "rebase <session>... | --all",
	Short: "Move sessions to the latest compatible workflow revision",
	Args: func(cmd *cobra.Command, args []string) error {
		if workflowRebaseAll && len(args) > 0 {
			return fmt.Errorf("--all cannot be combined with session names")
		}
		if !workflowRebaseAll && len(args) == 0 {
			return fmt.Errorf("provide session names or --all")
		}
		if workflowRebaseWorkflow != "" && !workflowRebaseAll {
			return fmt.Errorf("--workflow requires --all")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		if workflowRebaseAll {
			all, err := store.AllE()
			if err != nil {
				return err
			}
			for name, session := range all {
				if session.Status == contract.SessionStatusDestroyed || session.Workflow == "" {
					continue
				}
				if workflowRebaseWorkflow != "" && session.Workflow != workflowRebaseWorkflow {
					continue
				}
				args = append(args, name)
			}
		}
		sort.Strings(args)
		var failures []error
		for _, name := range args {
			result, err := service.WorkflowRebase(cfg, store, service.RebaseParams{Identifier: name, DryRun: workflowRebaseDryRun, WithRestart: workflowRebaseWithRestart, Observer: newTaskObserver(cfg)})
			if result != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s %s -> %s\n", result.SessionName, result.Class, result.From, result.To)
				if len(result.RecreateNodes) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  recreate: %s\n", strings.Join(result.RecreateNodes, ", "))
				}
				for _, reason := range result.Reasons {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", reason)
				}
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("rebase %q: %w", name, err))
			}
		}
		return errors.Join(failures...)
	},
}

func init() {
	workflowRebaseCmd.Flags().BoolVar(&workflowRebaseAll, "all", false, "Rebase all active sessions")
	workflowRebaseCmd.Flags().StringVar(&workflowRebaseWorkflow, "workflow", "", "Filter --all to this workflow")
	workflowRebaseCmd.Flags().BoolVar(&workflowRebaseDryRun, "dry-run", false, "Show classification and nodes to recreate without changing sessions")
	workflowRebaseCmd.Flags().BoolVar(&workflowRebaseWithRestart, "with-restart", false, "Allow old-revision down followed by new-revision up")
	workflowCmd.AddCommand(workflowRebaseCmd)
}
