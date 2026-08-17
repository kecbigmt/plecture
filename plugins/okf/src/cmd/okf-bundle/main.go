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

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: okf-bundle provider <setup|cleanup> [flags]")
	}
	group, sub, rest := args[0], args[1], args[2:]

	if group != "provider" {
		return fmt.Errorf("unknown subcommand %q %q; expected: provider setup, provider cleanup", group, sub)
	}
	switch sub {
	case "setup":
		return runProviderSetup(rest)
	case "cleanup":
		return runProviderCleanup(rest)
	}
	return fmt.Errorf("unknown subcommand %q %q; expected: provider setup, provider cleanup", group, sub)
}

func runProviderSetup(args []string) error {
	fs := flag.NewFlagSet("provider setup", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier (local-okf://<owner>/<concept-id>)")
	session := fs.String("session", "", "session name the workspace is acquired for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resourceID == "" || *session == "" {
		return fmt.Errorf("provider setup requires --resource and --session")
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

func runProviderCleanup(args []string) error {
	fs := flag.NewFlagSet("provider cleanup", flag.ContinueOnError)
	workspaceDir := fs.String("workspace-dir", "", "workspace directory recorded by provider setup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return workspace.Cleanup(*workspaceDir)
}

func encodeJSON(w *os.File, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}
