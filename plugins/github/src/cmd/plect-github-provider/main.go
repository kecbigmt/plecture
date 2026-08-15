// Command plect-github-provider is the executable the shipped GitHub
// provider and resource config invoke. Setup prints the provider outputs
// contract (a JSON object carrying the reserved `workdir` key) on stdout;
// cleanup prints nothing and reports failure via its exit status; observe
// prints the resource's observed state for resources/github.toml.
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
	"github.com/kecbigmt/plecture/plugins/github/src/internal/provider"
)

// ghClient builds the gh-api client setup/observe use, sharing the named
// github-watcher binary's rate budget when one is given.
func ghClient(watcherBin string) github.GHClient {
	if watcherBin == "" {
		return ghapi.Direct()
	}
	return ghapi.ViaWatcher(watcherBin)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "plect-github-provider:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: plect-github-provider <setup|cleanup|observe> [flags]")
	}
	ctx := context.Background()
	switch args[0] {
	case "setup":
		fs := flag.NewFlagSet("setup", flag.ContinueOnError)
		resource := fs.String("resource", "", "resource identifier (issue URL, pull request URL, or project item id)")
		session := fs.String("session", "", "session name the workdir is acquired for")
		workdirsRoot := fs.String("workdirs-root", "", "configured workdirs root")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set, otherwise call gh directly")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" || *session == "" {
			return fmt.Errorf("setup requires --resource and --session")
		}
		outputs, err := provider.Setup(ctx, provider.SetupOptions{ResourceID: *resource, SessionName: *session, WorkdirsRoot: *workdirsRoot, GHClient: ghClient(*watcherBin)})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(outputs)
	case "observe":
		fs := flag.NewFlagSet("observe", flag.ContinueOnError)
		resource := fs.String("resource", "", "resource identifier (issue URL or pull request URL)")
		workdirPath := fs.String("workdir-path", "", "the observing session's workdir, when one exists")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set, otherwise call gh directly")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" {
			return fmt.Errorf("observe requires --resource")
		}
		result, err := observe.Observe(ctx, observe.Options{ResourceID: *resource, WorkdirPath: *workdirPath, GHClient: ghClient(*watcherBin)})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"resource_kind":   result.ResourceKind,
			"checks_status":   result.ChecksStatus,
			"issue_status":    result.IssueStatus,
			"revision":        result.Revision,
			"pr_url":          result.PRURL,
			"mergeable_state": result.MergeableState,
		})
	case "cleanup":
		fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
		workdir := fs.String("workdir", "", "working directory recorded by setup")
		branch := fs.String("branch", "", "branch recorded by setup")
		workdirsRoot := fs.String("workdirs-root", "", "configured workdirs root")
		force := fs.Bool("force", false, "remove the workdir even when it carries uncommitted changes")
		deleteBranch := fs.Bool("delete-branch", false, "also delete the branch recorded by setup")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return provider.Cleanup(ctx, provider.CleanupOptions{Workdir: *workdir, Branch: *branch, WorkdirsRoot: *workdirsRoot, Force: *force, DeleteBranch: *deleteBranch})
	default:
		return fmt.Errorf("unknown subcommand %q; expected setup, cleanup, or observe", args[0])
	}
}
