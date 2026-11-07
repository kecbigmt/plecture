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
	Short: "Print the work directory for a session",
	Long:  "Print the session workflow's `workdir` to stdout. Use with: cd $(plect cd <resource-id|session>)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")

		wtPath, err := service.Workdir(cfg, store, args[0])
		if err != nil {
			return err
		}

		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			return fmt.Errorf("work directory not found: %s\nRun 'plect up' first", wtPath)
		}

		fmt.Println(wtPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cdCmd)
}
