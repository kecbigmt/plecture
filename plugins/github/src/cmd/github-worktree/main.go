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
		resource := fs.String("resource", "", "resource identifier (issue URL or pull request URL)")
		session := fs.String("session", "", "session name the workspace is acquired for")
		workspaceDirsRoot := fs.String("workspace-dirs-root", "", "configured workspace-dirs root")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set, otherwise call gh directly (overridden by app auth inputs below, when set)")
		layoutRoot := fs.String("workspace-layout-root", "", "root this provider lays repository worktree containers out under (default: the workspace-dirs root)")
		issueBranchTemplate := fs.String("issue-branch-template", "", "branch name for an issue resource, with {owner}/{repo}/{number} placeholders (default: issue/{number})")
		taggedBranchSuffix := fs.String("tagged-branch-suffix", "", "suffix appended to the branch for a tagged session, with a {tag} placeholder (default: +{tag})")
		appTokenBin := fs.String("app-token-bin", "", "path to gh-app-token; used only when app auth inputs are set, as the git credential helper")
		appID := fs.String("app-id", "", "GitHub App id for git and metadata-fetch authentication")
		installationID := fs.String("installation-id", "", "GitHub App installation id for git and metadata-fetch authentication")
		owner := fs.String("owner", "", "repository owner used to resolve a GitHub App installation id")
		repo := fs.String("repo", "", "repository name used to resolve a GitHub App installation id")
		privateKeyPath := fs.String("private-key-path", "", "path to the GitHub App private key for git and metadata-fetch authentication")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" || *session == "" {
			return fmt.Errorf("setup requires --resource and --session")
		}
		outputs, err := worktree.Setup(ctx, worktree.SetupOptions{
			ResourceID:          *resource,
			SessionName:         *session,
			WorkspaceDirsRoot:   *workspaceDirsRoot,
			WorkspaceLayoutRoot: *layoutRoot,
			IssueBranchTemplate: *issueBranchTemplate,
			TaggedBranchSuffix:  *taggedBranchSuffix,
			AppTokenBin:         *appTokenBin,
			AppID:               *appID,
			InstallationID:      *installationID,
			Owner:               *owner,
			Repo:                *repo,
			PrivateKeyPath:      *privateKeyPath,
			GHClient:            ghClient(*watcherBin),
		})
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
		deleteBranch := fs.String("delete-branch", "", "\"true\"/\"false\" to also delete the branch recorded by setup; empty defers to --delete-branch-default")
		deleteBranchDefault := fs.String("delete-branch-default", "", "\"true\" to delete the branch when the caller expressed no intent")
		layoutRoot := fs.String("workspace-layout-root", "", "root this provider lays repository worktree containers out under (default: the workspace-dirs root)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return worktree.Cleanup(ctx, worktree.CleanupOptions{
			WorkspaceDir:        *workspaceDir,
			Branch:              *branch,
			WorkspaceDirsRoot:   *workspaceDirsRoot,
			WorkspaceLayoutRoot: *layoutRoot,
			Force:               *force,
			DeleteBranch:        *deleteBranch,
			DeleteBranchDefault: *deleteBranchDefault,
		})
	default:
		return fmt.Errorf("unknown subcommand %q; expected setup or cleanup", args[0])
	}
}
