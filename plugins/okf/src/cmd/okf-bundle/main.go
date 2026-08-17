// Command okf-bundle is the executable the shipped okf plugin config
// invokes for its workspace provider hooks over the owner's knowledge
// bundle. Each subcommand prints the contract its TOML caller expects on
// stdout (JSON for setup) and reports failure through its exit status.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/plugins/okf/internal/cliexec"
	"github.com/kecbigmt/plecture/plugins/okf/internal/workspace"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "okf-bundle:", err)
		os.Exit(1)
	}
}

// run dispatches directly to setup/cleanup with no intervening "provider"
// subcommand group: the binary itself is the workspace provider realization
// (mirroring github-worktree's flat setup/cleanup shape), so a group noun
// naming that same concept again would be redundant.
func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: okf-bundle <setup|cleanup> [flags]")
	}
	sub, rest := args[0], args[1:]

	switch sub {
	case "setup":
		return runSetup(rest)
	case "cleanup":
		return runCleanup(rest)
	}
	return fmt.Errorf("unknown subcommand %q; expected: setup, cleanup", sub)
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier (local-okf://<owner>/<concept-id>)")
	session := fs.String("session", "", "session name the workspace is acquired for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resourceID == "" || *session == "" {
		return fmt.Errorf("setup requires --resource and --session")
	}

	result, err := workspace.Setup(cliexec.CLI{}, *resourceID, *session)
	if err != nil {
		return err
	}

	return encodeJSON(os.Stdout, map[string]any{
		"workspace_dir": result.WorkspaceDir,
		"owner":         result.Owner,
		"concept_id":    result.ConceptID,
		"concept_path":  result.ConceptPath,
	})
}

func runCleanup(args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	workspaceDir := fs.String("workspace-dir", "", "workspace directory recorded by setup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return workspace.Cleanup(*workspaceDir)
}

func encodeJSON(w *os.File, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}
