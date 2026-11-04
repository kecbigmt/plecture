package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var downCmd = &cobra.Command{
	Use:   "down <resource-id|session>",
	Short: "Fully stop a session or sleep it until an inbound event",
	Long: `Run cleanup commands for all run-scoped tasks in reverse dependency order.
Session-scoped tasks are preserved. A plain down is a full stop: inbound
events stay in the log and are forwarded to an ancestor without waking the
session. --sleep wakes for the next inbound event when capacity permits.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		result, err := service.Down(cfg, store, service.DownParams{
			Identifier: args[0],
			Observer:   newTaskObserver(cfg),
			Sleep:      downSleep,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Session: %s\n", result.SessionName)
		return nil
	},
}

var downSleep bool

func init() {
	downCmd.Flags().BoolVar(&downSleep, "sleep", false, "Wake on the next inbound event, subject to available capacity")
	rootCmd.AddCommand(downCmd)
}
