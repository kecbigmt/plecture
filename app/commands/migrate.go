package commands

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/migrate"
	"github.com/kecbigmt/sennit/app/internal/state"
)

var migrateDataDir string
var migrateConfigPath string

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "One-time rewrite of legacy state/config data into its current form",
	Long: `Rewrites state.json and config.toml from the legacy forms superseded by
earlier releases (legacy GitHub identity fields, the legacy slack/effects
state fields, inline-task sessions with an implicit task_id, and the
repo_allowlist config key) into their current forms.

This is a one-time operator step, not something sennit runs on its own: run
it once after upgrading, before any release that removes support for the
legacy forms. Before rewriting anything it copies the current state.json and
config.toml into a timestamped backup directory, so the run can be undone by
copying those files back (see the migration procedure document for details).

Running it again against already-migrated data is a no-op.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		statePath := filepath.Join(state.NewStore(migrateDataDir).Dir(), "state.json")
		configPath := migrateConfigPath
		if configPath == "" {
			configPath = config.DefaultPath()
		}

		report, err := migrate.Run(migrate.Options{
			StatePath:  statePath,
			ConfigPath: configPath,
		})
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if !report.Changed {
			fmt.Fprintln(out, "nothing to do: state.json and config.toml are already in the current form")
			return nil
		}

		fmt.Fprintf(out, "backup written to %s\n", report.BackupDir)
		for _, note := range report.Notes {
			fmt.Fprintln(out, note)
		}
		return nil
	},
}

func init() {
	migrateCmd.Flags().StringVar(&migrateDataDir, "data-dir", "", "Directory containing state.json (default: $XDG_DATA_HOME/sennit or ~/.local/share/sennit)")
	migrateCmd.Flags().StringVar(&migrateConfigPath, "config", "", "Path to config.toml (default: ~/.config/sennit/config.toml)")
	rootCmd.AddCommand(migrateCmd)
}
