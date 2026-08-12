// legacy-migration is a standalone, one-time operator tool that rewrites a
// plecture data directory's state.json and config.toml from the legacy,
// GitHub-shaped forms produced by earlier plecture releases into their
// current forms, ahead of the follow-up changes that remove the code which
// still reads the legacy forms.
//
// This tool is not part of the core plecture CLI: it is a throwaway,
// transitional plugin. Once the migration has been run against every data
// directory that needs it, this whole plugin can be deleted along with the
// legacy field knowledge it embeds.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/plugins/legacy-migration/internal/migrate"
)

func main() {
	dataDir := flag.String("data-dir", "", "directory containing state.json (default: $XDG_DATA_HOME/plecture or ~/.local/share/plecture)")
	configPath := flag.String("config", "", "path to config.toml (default: ~/.config/plecture/config.toml)")
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

// resolveDataDir mirrors plecture's own default data-directory resolution
// ($XDG_DATA_HOME/plecture, falling back to ~/.local/share/plecture) without
// depending on the core plecture module, which this plugin does not import.
func resolveDataDir(override string) string {
	if override != "" {
		return override
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "plecture")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".local/share/plecture"
	}
	return filepath.Join(home, ".local", "share", "plecture")
}

// defaultConfigPath mirrors plecture's own default config path
// (~/.config/plecture/config.toml).
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "plecture", "config.toml")
}
