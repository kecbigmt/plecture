package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "sennit",
	Short: "Manage runtime sessions and git worktrees for resource-driven workflows",
	Long: `sennit manages runtime sessions and git worktrees for workflows keyed by a
resource identifier.

A workflow's [resolver] maps the resource identifier to a session id and
working directory; sennit then runs the workflow's tasks (worktree setup, runtime
session, agent launch) and manages the full session lifecycle.

The resource identifier is any string a workflow resolver accepts. Which
identifiers resolve out of the box depends on the providers installed; an
identifier no resolver matches selects a workflow explicitly (see
'sennit workflow list').`,
	// Cobra prints usage after any RunE error by default, which is noise for
	// runtime failures (a failed task is not an argument-parsing mistake).
	// We still want the "Error: ..." line that Cobra prints, just not the
	// help dump that follows.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	err := rootCmd.Execute()
	if err == nil {
		return nil
	}
	if hint := removedLifecycleCommandHint(os.Args[1:]); hint != "" {
		err = fmt.Errorf("%w\n%s", err, hint)
	}
	rootCmd.PrintErrln(rootCmd.ErrPrefix(), err.Error())
	return err
}

func removedLifecycleCommandHint(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "create":
		return "Use `sennit up <resource-id>` instead."
	default:
		return ""
	}
}
