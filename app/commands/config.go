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

// configHomeSource must check configHomeFlag before the env var: by the time
// this runs, PersistentPreRunE has already exported a nonempty flag value
// into the env var, so checking the env var first would misattribute a
// flag-driven override to PLECT_CONFIG_HOME.
func configHomeSource() string {
	switch {
	case configHomeFlag != "":
		return "--config-home flag"
	case os.Getenv(confighome.EnvVar) != "":
		return confighome.EnvVar + " env var"
	case os.Getenv(confighome.XDGEnvVar) != "":
		return confighome.XDGEnvVar + " env var"
	default:
		return "default"
	}
}

func init() {
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}
