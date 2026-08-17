package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var cdCmd = &cobra.Command{
	Use:   "cd <resource-id|session>",
	Short: "Print the workspace directory for a session",
	Long:  "Print the workspace directory to stdout. Use with: cd $(plect cd <resource-id|session>)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")

		// Full state v3 lookup (name → alias → legacy URL → resolver
		// derivation), with a workspace-provider-resolver fallback for
		// URL-shaped inputs that have no state entry. The state-recorded
		// workspace directory wins because, for workflow-setup sessions, it
		// is declared by the setup script and not derivable from the path
		// convention.
		wtPath, err := service.WorkspaceDir(cfg, store, args[0])
		if err != nil {
			return err
		}

		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			return fmt.Errorf("workspace directory not found: %s\nRun 'plect up' first", wtPath)
		}

		fmt.Println(wtPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cdCmd)
}
