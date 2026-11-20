package commands

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/inbox"
)

var inboxTakeWait time.Duration

// inboxPollInterval trades take latency against one cheap read per interval
// for every waiting runtime.
const inboxPollInterval = 250 * time.Millisecond

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Take and acknowledge the events pending for a session's runtime",
	Long: `A session's inbox holds the events its inbox channel selected and its runtime
has not acknowledged. A runtime takes everything pending as one batch, handles
it, and acknowledges the batch.`,
}

var inboxTakeCmd = &cobra.Command{
	Use:   "take <session>",
	Short: "Take the pending events as one batch",
	Long: `Prints one JSON object: "batch" (the id to acknowledge), "deliveries" (how
often this batch has been handed out, 1 on the first take) and "events" (in log
order). An event another event of the batch supersedes is left out. Prints
nothing when nothing is pending.

Until a batch is acknowledged, take returns the same batch again and counts one
more delivery. A batch delivered 3 times without an acknowledgement is parked:
the next take records a plect.channel.error for each of its events and returns
the following batch.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := inbox.NewStore("")
		deadline := time.Now().Add(inboxTakeWait)
		for {
			// The wait polls with a read: a take is a write transaction, and
			// every idle runtime would otherwise contend for the write lock.
			pending, err := store.Pending(args[0])
			if err != nil {
				return err
			}
			if pending {
				batch, err := store.Take(args[0])
				if err != nil {
					return err
				}
				if batch != nil {
					out, err := json.Marshal(batch)
					if err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), string(out))
					return nil
				}
			}
			if !time.Now().Before(deadline) {
				return nil
			}
			time.Sleep(inboxPollInterval)
		}
	},
}

var inboxAckCmd = &cobra.Command{
	Use:   "ack <session> <batch>",
	Short: "Acknowledge a taken batch",
	Long: `Removes the batch's events from the inbox. Fails when the session has no
outstanding batch with that id.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return inbox.NewStore("").Ack(args[0], args[1])
	},
}

func init() {
	inboxTakeCmd.Flags().DurationVar(&inboxTakeWait, "wait", 0, "How long to wait for a pending event before returning nothing")
	inboxCmd.AddCommand(inboxTakeCmd, inboxAckCmd)
	rootCmd.AddCommand(inboxCmd)
}
