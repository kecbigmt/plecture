package commands

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var subscriptionSession string
var subscriptionResourceType string
var subscriptionJSON bool
var unsubscribeSession string

var subscriptionCmd = &cobra.Command{
	Use:   "subscription",
	Short: "Inspect resource subscriptions",
}

var subscriptionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List subscriptions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rows, err := state.NewStore("").ListSubscriptions(subscriptionSession, subscriptionResourceType)
		if err != nil {
			return err
		}
		if subscriptionJSON {
			return printJSON(rows)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SESSION\tRESOURCE\tSOURCE")
		for _, row := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\n", row.Session, row.Resource.Locator, row.Source)
		}
		return w.Flush()
	},
}

var unsubscribeCmd = &cobra.Command{
	Use:   "unsubscribe <resource-id>",
	Short: "Remove the current session's runtime subscription",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := service.Unsubscribe(cfg, state.NewStore(""), service.SubscribeParams{ResourceID: args[0], SessionName: unsubscribeSession}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Unsubscribed from %s\n", args[0])
		return nil
	},
}

func init() {
	subscriptionListCmd.Flags().StringVar(&subscriptionSession, "session", "", "Filter by session")
	subscriptionListCmd.Flags().StringVar(&subscriptionResourceType, "resource-type", "", "Filter by resource type")
	subscriptionListCmd.Flags().BoolVar(&subscriptionJSON, "json", false, "Output as JSON")
	subscriptionCmd.AddCommand(subscriptionListCmd)
	rootCmd.AddCommand(subscriptionCmd)
	unsubscribeCmd.Flags().StringVar(&unsubscribeSession, "session", "", "Subscriber session (defaults to $PLECT_SESSION_NAME)")
	rootCmd.AddCommand(unsubscribeCmd)
}
