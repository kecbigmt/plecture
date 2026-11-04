package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var (
	subscribeSession    string
	subscribeInputsJSON string
	subscribeInputsFile string
)

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <resource-id>",
	Short: "Subscribe the current session to a resource's events",
	Long: `Subscribe the current session to an opaque resource so its events arrive
in its own event log (for a code review resource: CI status, review decisions,
state changes — readable with 'plect event list').

This is the runtime counterpart to the auto-subscribe that runs at dispatch
time: it ADDS a subscription to a live session without recreating it, and
never replaces existing subscriptions. Subscribing a PR another session is
already watching does not take it over — each subscriber receives its own
copy of the events in its own log.

The subscriber session is taken from the ambient pane environment
($PLECT_SESSION_NAME, exported into the agent's shell), so a running agent can
simply 'plect subscribe <url>'. --session overrides it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := resolveInputsFlags(subscribeInputsJSON, subscribeInputsFile)
		if err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		if err := service.Subscribe(cfg, store, service.SubscribeParams{
			ResourceID:  args[0],
			SessionName: subscribeSession,
			Inputs:      inputs,
		}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Subscribed to %s\n", args[0])
		return nil
	},
}

func init() {
	subscribeCmd.Flags().StringVar(&subscribeSession, "session", "", "Subscriber session (defaults to $PLECT_SESSION_NAME)")
	subscribeCmd.Flags().StringVar(&subscribeInputsJSON, "inputs", "", "Resource action inputs as a JSON object")
	subscribeCmd.Flags().StringVar(&subscribeInputsFile, "inputs-file", "", "Read resource action inputs from a JSON file")
	rootCmd.AddCommand(subscribeCmd)
}
