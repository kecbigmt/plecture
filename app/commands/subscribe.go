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
	subscribeSession string
)

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <resource-id>",
	Short: "Subscribe the current session to a resource's events",
	Long: `Subscribe the current session to an opaque resource so its events arrive
in its own event log (for a code review resource: CI status, review decisions,
state changes — readable with 'sennit event list').

This is the runtime counterpart to the auto-subscribe that runs at dispatch
time: it ADDS a subscription to a live session without recreating it, and
never replaces existing subscriptions. Subscribing a PR another session is
already watching does not take it over — each subscriber receives its own
copy of the events in its own log.

The subscriber session is taken from the ambient pane environment
($SENNIT_SESSION_NAME, exported into the agent's shell), so a running agent can
simply 'sennit subscribe <url>'. --session overrides it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		if err := service.Subscribe(cfg, store, service.SubscribeParams{
			ResourceID:  args[0],
			SessionName: subscribeSession,
		}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Subscribed to %s\n", args[0])
		return nil
	},
}

func init() {
	subscribeCmd.Flags().StringVar(&subscribeSession, "session", "", "Subscriber session (defaults to $SENNIT_SESSION_NAME)")
	rootCmd.AddCommand(subscribeCmd)
}
