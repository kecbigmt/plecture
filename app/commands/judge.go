package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/service"
	"github.com/plecture/plect/app/internal/state"
	"github.com/plecture/plect/app/internal/task"
)

var (
	judgeReason          string
	judgeRevision        string
	judgeReviewerSession string
)

var judgeCmd = &cobra.Command{
	Use:   "judge",
	Short: "Record done_when judge actions",
}

func newJudgeActionCmd(use, action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <session> <instance> <judge-id>",
		Short: short,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := service.RecordJudge(config.Load(), state.NewStore(""), service.JudgeParams{
				SessionName:     args[0],
				Instance:        args[1],
				LeafID:          args[2],
				Action:          action,
				Reason:          judgeReason,
				Revision:        judgeRevision,
				ReviewerSession: judgeReviewerSession,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s for %s/%s judge %s at revision %s\n", result.Action, result.SessionName, result.Instance, result.LeafID, result.Revision)
			return nil
		},
	}
}

func init() {
	approveCmd := newJudgeActionCmd("approve", task.JudgeActionApprove, "Approve one done_when judge")
	requestChangesCmd := newJudgeActionCmd("request-changes", task.JudgeActionRequestChanges, "Request changes for one done_when judge")
	for _, cmd := range []*cobra.Command{approveCmd, requestChangesCmd} {
		cmd.Flags().StringVar(&judgeReason, "reason", "", "Reason for this judge action")
		cmd.Flags().StringVar(&judgeRevision, "revision", "", "Opaque revision reviewed (defaults to the instance revision output)")
		cmd.Flags().StringVar(&judgeReviewerSession, "reviewer-session", "", "Reviewer session name (defaults to $PLECT_SESSION_NAME; provenance-constrained judges require it to match the ambient reviewer pane)")
		cmd.MarkFlagRequired("reason")
		judgeCmd.AddCommand(cmd)
	}

	rootCmd.AddCommand(judgeCmd)
}
