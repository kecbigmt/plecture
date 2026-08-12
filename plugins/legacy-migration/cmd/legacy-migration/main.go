// legacy-migration is a standalone, one-time operator tool that rewrites a
// plect data directory's state.json and config.toml from the legacy,
// GitHub-shaped forms produced by earlier plect releases into their
// current forms, ahead of the follow-up changes that remove the code which
// still reads the legacy forms.
//
// This tool is not part of the core plect CLI: it is a throwaway,
// transitional plugin. Once the migration has been run against every data
// directory that needs it, this whole plugin can be deleted along with the
// legacy field knowledge it embeds.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plect/plugins/legacy-migration/internal/migrate"
)

func main() {
	dataDir := flag.String("data-dir", "", "directory containing state.json (default: $XDG_DATA_HOME/plect or ~/.local/share/plect)")
	configPath := flag.String("config", "", "path to config.toml (default: ~/.config/plect/config.toml)")
	flag.Parse()

	statePath := filepath.Join(resolveDataDir(*dataDir), "state.json")
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}

	report, err := migrate.Run(migrate.Options{
		StatePath:  statePath,
		ConfigPath: cfgPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "legacy-migration: %v\n", err)
		os.Exit(1)
	}

	if !report.Changed {
		fmt.Println("nothing to do: state.json and config.toml are already in the current form")
		return
	}

	fmt.Printf("backup written to %s\n", report.BackupDir)
	for _, note := range report.Notes {
		fmt.Println(note)
	}
}

// resolveDataDir mirrors plect's own default data-directory resolution
// ($XDG_DATA_HOME/plect, falling back to ~/.local/share/plect) without
// depending on the core plect module, which this plugin does not import.
func resolveDataDir(override string) string {
	if override != "" {
		return override
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "plect")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".local/share/plect"
	}
	return filepath.Join(home, ".local", "share", "plect")
}

// defaultConfigPath mirrors plect's own default config path
// (~/.config/plect/config.toml).
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "plect", "config.toml")
}
