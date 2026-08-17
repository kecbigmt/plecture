// Command github-worktree is the executable the shipped GitHub workspace
// provider config invokes. Setup prints the workspace provider outputs
// contract (a JSON object carrying the reserved `workspace_dir` key) on
// stdout; cleanup prints nothing and reports failure via its exit status.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/worktree"
)

// ghClient builds the gh-api client setup uses, sharing the named
// github-watcher binary's rate budget when one is given.
func ghClient(watcherBin string) github.GHClient {
	if watcherBin == "" {
		return ghapi.Direct()
	}
	return ghapi.ViaWatcher(watcherBin)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "github-worktree:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: github-worktree <setup|cleanup> [flags]")
	}
	ctx := context.Background()
	switch args[0] {
	case "setup":
		fs := flag.NewFlagSet("setup", flag.ContinueOnError)
		resource := fs.String("resource", "", "resource identifier (issue URL, pull request URL, or project item id)")
		session := fs.String("session", "", "session name the workspace is acquired for")
		workspaceDirsRoot := fs.String("workspace-dirs-root", "", "configured workspace-dirs root")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set, otherwise call gh directly")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" || *session == "" {
			return fmt.Errorf("setup requires --resource and --session")
		}
		outputs, err := worktree.Setup(ctx, worktree.SetupOptions{ResourceID: *resource, SessionName: *session, WorkspaceDirsRoot: *workspaceDirsRoot, GHClient: ghClient(*watcherBin)})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(outputs)
	case "cleanup":
		fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
		workspaceDir := fs.String("workspace-dir", "", "workspace directory recorded by setup")
		branch := fs.String("branch", "", "branch recorded by setup")
		workspaceDirsRoot := fs.String("workspace-dirs-root", "", "configured workspace-dirs root")
		force := fs.Bool("force", false, "remove the workspace directory even when it carries uncommitted changes")
		deleteBranch := fs.Bool("delete-branch", false, "also delete the branch recorded by setup")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return worktree.Cleanup(ctx, worktree.CleanupOptions{WorkspaceDir: *workspaceDir, Branch: *branch, WorkspaceDirsRoot: *workspaceDirsRoot, Force: *force, DeleteBranch: *deleteBranch})
	default:
		return fmt.Errorf("unknown subcommand %q; expected setup or cleanup", args[0])
	}
}
