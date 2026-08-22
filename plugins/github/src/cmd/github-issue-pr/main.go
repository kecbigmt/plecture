// Command github-issue-pr is the executable the shipped GitHub resource
// config invokes. Observe prints the observed state of whichever kind the
// identifier names, for resources/issue.toml or resources/pull.toml.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/observe"
)

// ghClient builds the gh-api client observe uses, sharing the named
// github-watcher binary's rate budget when one is given.
func ghClient(watcherBin string) github.GHClient {
	if watcherBin == "" {
		return ghapi.Direct()
	}
	return ghapi.ViaWatcher(watcherBin)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "github-issue-pr:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: github-issue-pr observe [flags]")
	}
	ctx := context.Background()
	switch args[0] {
	case "observe":
		fs := flag.NewFlagSet("observe", flag.ContinueOnError)
		resource := fs.String("resource", "", "resource identifier (issue URL or pull request URL)")
		workspaceDirPath := fs.String("workspace-dir-path", "", "the observing session's workspace directory, when one exists")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set, otherwise call gh directly")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" {
			return fmt.Errorf("observe requires --resource")
		}
		result, err := observe.Observe(ctx, observe.Options{ResourceID: *resource, WorkspaceDirPath: *workspaceDirPath, GHClient: ghClient(*watcherBin)})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result.Document())
	default:
		return fmt.Errorf("unknown subcommand %q; expected observe", args[0])
	}
}
