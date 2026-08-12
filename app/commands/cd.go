package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/service"
	"github.com/plecture/plect/app/internal/state"
)

var cdCmd = &cobra.Command{
	Use:   "cd <resource-id|session>",
	Short: "Print the working directory for a session",
	Long:  "Print the working directory to stdout. Use with: cd $(plect cd <resource-id|session>)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")

		// Full state v3 lookup (name → alias → legacy URL → resolver
		// derivation), with a provider-resolver fallback for URL-shaped
		// inputs that have no state entry. The state-recorded workdir wins
		// because, for workflow-setup sessions, it is declared by the setup
		// script and not derivable from the path convention.
		wtPath, err := service.Workdir(cfg, store, args[0])
		if err != nil {
			return err
		}

		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			return fmt.Errorf("worktree not found: %s\nRun 'plect up' first", wtPath)
		}

		fmt.Println(wtPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cdCmd)
}
