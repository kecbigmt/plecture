package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/confighome"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect resolved config locations",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show which config home is active and how it was determined",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := confighome.Resolve()
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Config home: %s (%s)\n", home, configHomeSource())
		return nil
	},
}

// configHomeSource names the precedence layer that determined the active
// config home, mirroring PersistentPreRunE's flag-then-env resolution: the
// flag has already been exported to the env var by the time this runs, so
// the flag must be checked first to attribute it correctly.
func configHomeSource() string {
	switch {
	case configHomeFlag != "":
		return "--config-home flag"
	case os.Getenv(confighome.EnvVar) != "":
		return confighome.EnvVar + " env var"
	default:
		return "default"
	}
}

func init() {
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}
