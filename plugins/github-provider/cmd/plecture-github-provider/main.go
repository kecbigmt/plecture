// Command plecture-github-provider is the executable the shipped GitHub
// provider config invokes for its setup and cleanup hooks. Setup prints the
// provider outputs contract (a JSON object carrying the reserved `workdir`
// key) on stdout; cleanup prints nothing and reports failure via its exit
// status.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/provider"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "plecture-github-provider:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: plecture-github-provider <setup|cleanup> [flags]")
	}
	ctx := context.Background()
	switch args[0] {
	case "setup":
		fs := flag.NewFlagSet("setup", flag.ContinueOnError)
		resource := fs.String("resource", "", "resource identifier (issue URL, pull request URL, or project item id)")
		session := fs.String("session", "", "session name the worktree is acquired for")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resource == "" || *session == "" {
			return fmt.Errorf("setup requires --resource and --session")
		}
		outputs, err := provider.Setup(ctx, provider.SetupOptions{ResourceID: *resource, SessionName: *session})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(outputs)
	case "cleanup":
		fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
		workdir := fs.String("workdir", "", "working directory recorded by setup")
		branch := fs.String("branch", "", "branch recorded by setup")
		force := fs.Bool("force", false, "remove the worktree even when it carries uncommitted changes")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return provider.Cleanup(ctx, provider.CleanupOptions{Workdir: *workdir, Branch: *branch, Force: *force})
	default:
		return fmt.Errorf("unknown subcommand %q; expected setup or cleanup", args[0])
	}
}
