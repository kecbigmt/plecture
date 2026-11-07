package commands

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
)

var (
	resourceStatusJSON       bool
	resourceStatusInputs     string
	resourceStatusInputsFile string
)

var resourceCmd = &cobra.Command{
	Use:   "resource",
	Short: "Observe external resources by resource id",
}

var resourceStatusCmd = &cobra.Command{
	Use:   "status <resource-id>",
	Short: "Observe a resource's current state",
	Long: `Finds the trusted resource definition (resources/*.toml in the plugin/global
config layers) whose 'match' recognizes <resource-id>, runs its 'observe'
script, and reports the result. A resource id with no matching definition is
an error — declare one (ADR "goal-as-task" D1) before observing it standalone.

This is the same observation contract a task instance's from_resource_status
dynamic output reads from ('plect task setup --resource'); this command lets it
be read outside any one task instance.

--inputs / --inputs-file supply the resource's action input object, checked
against its inputs_schema. A resource that declares no inputs_schema accepts
only the empty object.

Example:
  plect resource status <resource-id>`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := resolveInputsFlags(resourceStatusInputs, resourceStatusInputsFile)
		if err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		result, err := service.ResourceStatus(cfg, service.ResourceStatusParams{ResourceID: args[0], Inputs: inputs})
		if err != nil {
			return err
		}
		if resourceStatusJSON {
			b, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "resource:\t%s\n", result.ResourceID)
		fmt.Fprintf(cmd.OutOrStdout(), "definition:\t%s\n", result.Definition)
		keys := make([]string, 0, len(result.State))
		for k := range result.State {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(cmd.OutOrStdout(), "%s:\t%v\n", k, result.State[k])
		}
		return nil
	},
}

func init() {
	resourceStatusCmd.Flags().BoolVar(&resourceStatusJSON, "json", false, "Output as JSON")
	resourceStatusCmd.Flags().StringVar(&resourceStatusInputs, "inputs", "", "Resource action inputs as a JSON object string")
	resourceStatusCmd.Flags().StringVar(&resourceStatusInputsFile, "inputs-file", "", "Path to a JSON file containing the resource action inputs object")
	resourceCmd.AddCommand(resourceStatusCmd)
	rootCmd.AddCommand(resourceCmd)
}
