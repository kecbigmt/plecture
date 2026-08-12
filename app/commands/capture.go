package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/service"
	"github.com/plecture/plect/app/internal/state"
)

var captureCmd = &cobra.Command{
	Use:   "capture <url|session>",
	Short: "Print a read-only snapshot of a session's channel",
	Long: `Resolve the session, find the task declaring 'capture', run its
template, and print the output as-is.

Symmetric with 'attach': attach hands the terminal over, capture only reads
it. No interpretation — the raw view is returned for the caller (human or
LLM) to judge. Session state never changes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.Capture(cfg, store, service.CaptureParams{Identifier: args[0]})
		if err != nil {
			return err
		}
		fmt.Print(result.Content)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(captureCmd)
}
