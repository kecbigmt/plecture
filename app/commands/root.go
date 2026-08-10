package commands

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tws",
	Short: "Manage runtime sessions and git worktrees for resource-driven workflows",
	Long: `tws manages runtime sessions and git worktrees for workflows keyed by a
resource identifier.

A workflow's [resolver] maps the resource identifier to a session id and
working directory; tws then runs the workflow's tasks (worktree setup, runtime
session, agent launch) and manages the full session lifecycle.

The resource identifier is any string a workflow resolver accepts. GitHub
Issue/PR URLs and PVTI_xxx project items resolve out of the box; other
identifiers select a workflow explicitly (see 'tws workflow list').

Example resource identifiers:
  https://github.com/<owner>/<repo>/issues/<number>
  https://github.com/<owner>/<repo>/pull/<number>`,
	// Cobra prints usage after any RunE error by default, which is noise for
	// runtime failures (a failed task is not an argument-parsing mistake).
	// We still want the "Error: ..." line that Cobra prints, just not the
	// help dump that follows.
	SilenceUsage: true,
}

func Execute() error {
	return rootCmd.Execute()
}
