package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var watchdogJSON bool

var watchdogCmd = &cobra.Command{
	Use:   "watchdog",
	Short: "Layer-2 liveness probes (ADR: cross-session terminal event propagation)",
}

var watchdogCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Probe every up session and push a dead event for the unhealthy ones",
	Long: `Runs each produced run-scoped task's declared healthcheck for every session
that currently has a run scope up. An unhealthy session gets a dead terminal
event pushed one hop to its immediate parent, skipping over a dead
intermediate parent to the next live ancestor (ADR D4). Idempotent per
unhealthy session (event id dedup), so invoking it repeatedly — e.g. from a
scheduled timer — does not re-push the same fact.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		reports, err := service.WatchdogTick(config.Load(), state.NewStore(""))
		if err != nil {
			return err
		}
		if watchdogJSON {
			b, err := json.MarshalIndent(reports, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		for _, r := range reports {
			status := "healthy"
			if !r.Healthy {
				status = "dead: " + r.Reason
				if r.Pushed {
					status += fmt.Sprintf(" (pushed to %s)", r.PushTarget)
					if r.WakeWarning != "" {
						status += fmt.Sprintf(" [wake warning: %s]", r.WakeWarning)
					}
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", r.SessionName, status)
		}
		return nil
	},
}

func init() {
	watchdogCheckCmd.Flags().BoolVar(&watchdogJSON, "json", false, "Output health reports as JSON")
	watchdogCmd.AddCommand(watchdogCheckCmd)
	rootCmd.AddCommand(watchdogCmd)
}
