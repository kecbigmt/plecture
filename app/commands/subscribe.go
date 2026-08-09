package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var (
	subscribeSession string
	subscribeStream  string
)

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <resource-id>",
	Short: "Subscribe the current session to a resource's events",
	Long: `Subscribe the current session to an opaque resource so its events arrive
in this session's event stream (for GitHub PRs/issues: CI status, review
decisions, state changes — readable with 'tws event list').

This is the runtime counterpart to the auto-subscribe that runs at dispatch
time: it ADDS a subscription to a live session without recreating it, and
never replaces existing subscriptions. Subscribing a PR another session is
already watching does not take it over — each subscriber receives the events
on its own stream.

The subscriber session and its work-stream are taken from the ambient pane
environment ($TWS_SESSION_NAME / $TWS_STREAM_ID, exported into the agent's
shell), so a running agent can simply 'tws subscribe <url>'. --session /
--stream override them.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		if err := service.Subscribe(cfg, store, service.SubscribeParams{
			ResourceID:  args[0],
			SessionName: subscribeSession,
			StreamID:    subscribeStream,
		}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Subscribed to %s\n", args[0])
		return nil
	},
}

func init() {
	subscribeCmd.Flags().StringVar(&subscribeSession, "session", "", "Subscriber session (defaults to $TWS_SESSION_NAME)")
	subscribeCmd.Flags().StringVar(&subscribeStream, "stream", "", "Work-stream id stamped on the resource's events (defaults to $TWS_STREAM_ID)")
	rootCmd.AddCommand(subscribeCmd)
}
