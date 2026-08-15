package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/service"
)

var (
	pluginUpdateRevision string
	pluginVerifyLocked   bool
	pluginListJSON       bool
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage plugins enabled from registered catalogs",
}

var pluginAddCmd = &cobra.Command{
	Use:   "add <alias>/<path>",
	Short: "Enable one plugin path from an already-registered catalog",
	Long: `Enable the plugin at <path> from the catalog registered as <alias>
(register it first with "plect catalog add"). Reuses the catalog's existing
lock coordinate when one exists, so enabling a second plugin from an
already-added catalog needs no new fetch. Never prompts — trust was already
established when the catalog itself was registered.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.PluginAdd(cmd.Context(), mustPluginPaths(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "added %s: revision=%s content_hash=%s\n", result.ID, orNone(result.ResolvedRevision), result.ContentHash)
		return nil
	},
}

var pluginUpdateCmd = &cobra.Command{
	Use:   "update <alias>/<path>",
	Short: "Repoint one enabled plugin to a fresh catalog snapshot",
	Long: `Fetch the newest matching catalog snapshot (--revision is required for a
git-sourced catalog; rejected for a path-sourced one) and rewrite only this
plugin's lock entry. Other plugins enabled from the same catalog keep their
previously locked coordinates.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.PluginUpdate(cmd.Context(), mustPluginPaths(), args[0], pluginUpdateRevision)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "updated %s: revision=%s content_hash=%s\n", result.ID, orNone(result.ResolvedRevision), result.ContentHash)
		return nil
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <alias>/<path>",
	Short: "Disable one enabled plugin",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.PluginRemove(mustPluginPaths(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", result.ID)
		return nil
	},
}

var pluginVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Re-resolve every enabled plugin and compare it against plect.lock",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := service.PluginVerify(mustPluginPaths(), pluginVerifyLocked)
		if err != nil {
			return err
		}
		if err := writePluginVerify(cmd.OutOrStdout(), result); err != nil {
			return err
		}
		if !result.AllOK {
			return fmt.Errorf("one or more plugins failed verification")
		}
		return nil
	},
}

func writePluginVerify(out io.Writer, result *service.PluginVerifyResult) error {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS")
	for _, e := range result.Entries {
		status := "ok"
		switch {
		case e.NonReproducible:
			status = "non-reproducible (editable)"
		case !e.OK:
			status = "FAILED: " + e.Error
		}
		fmt.Fprintf(w, "%s\t%s\n", e.ID, status)
	}
	return w.Flush()
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show enabled, resolved, locked, and compatibility state for every plugin",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := service.PluginList(mustPluginPaths())
		if err != nil {
			return err
		}
		if pluginListJSON {
			b, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		return writePluginList(cmd.OutOrStdout(), entries)
	},
}

func writePluginList(out io.Writer, entries []service.PluginListEntry) error {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREVISION\tSTATUS")
	for _, e := range entries {
		revision := e.ResolvedRevision
		if e.NonReproducible {
			revision = "(editable, non-reproducible)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.ID, orNone(revision), e.Status)
	}
	return w.Flush()
}

func init() {
	pluginUpdateCmd.Flags().StringVar(&pluginUpdateRevision, "revision", "", "New git revision to resolve (required for a git-sourced catalog)")
	pluginVerifyCmd.Flags().BoolVar(&pluginVerifyLocked, "locked", false, "Skip plugins from an editable path catalog, which are never pinned or verified")
	pluginListCmd.Flags().BoolVar(&pluginListJSON, "json", false, "Output as JSON")

	pluginCmd.AddCommand(pluginAddCmd, pluginUpdateCmd, pluginRemoveCmd, pluginVerifyCmd, pluginListCmd)
	rootCmd.AddCommand(pluginCmd)
}
