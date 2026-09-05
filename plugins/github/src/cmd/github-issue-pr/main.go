// Command github-issue-pr is the executable the shipped GitHub resource
// config invokes. Observe prints the observed state of whichever kind the
// identifier names, for resources/issue.toml or resources/pull.toml.
// Query-pulls prints one complete JSON array of pull requests matching a
// set of query parameters — the pull resource observer's query.poll means.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/observe"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/pullquery"
)

// ghClient builds the gh-api client observe uses, sharing the named
// github-watcher binary's rate budget when one is given.
func ghClient(watcherBin string) github.GHClient {
	if watcherBin == "" {
		return ghapi.Direct()
	}
	return ghapi.ViaWatcher(watcherBin)
}

// queryClient favors deployment-level GitHub App auth (the same
// GITHUB_WATCHER_APP_* env github-watcher serve reads) over the plain
// gh-api client: the query means run standalone, invoked by the resident
// evaluator rather than a session's own task, so there is no per-call
// app_id/private_key_path input to select a token by — see the README's
// "App auth" section.
func queryClient(watcherBin, cachePathOverride string) (github.GHClient, error) {
	appClient, err := ghapi.AppFromEnv(defaultAppCachePath(cachePathOverride))
	if err != nil {
		return nil, fmt.Errorf("github app auth: %w", err)
	}
	if appClient != nil {
		return appClient, nil
	}
	return ghClient(watcherBin), nil
}

// defaultAppCachePath mirrors github-watcher's own default data directory
// so a deployment running both serve and query-pulls mints one installation
// token instead of two.
func defaultAppCachePath(override string) string {
	if override != "" {
		return override
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "github-watcher", ".app-token-cache.json")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "github-issue-pr:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: github-issue-pr <observe|query-pulls> [flags]")
	}
	ctx := context.Background()
	switch args[0] {
	case "query-pulls":
		fs := flag.NewFlagSet("query-pulls", flag.ContinueOnError)
		repositories := fs.String("repositories", "[]", "JSON array of \"owner/repo\" strings to match")
		labels := fs.String("labels", "[]", "JSON array of label names every matched pull request must carry")
		state := fs.String("state", "", "\"open\", \"closed\", or \"all\"")
		draft := fs.String("draft", "", "\"true\"/\"false\": match only pull requests whose draft flag equals this")
		watcherBin := fs.String("watcher-bin", "", "path to a github-watcher binary; gh-api calls route through its shared rate budget when set and no App auth env is configured, otherwise call gh directly")
		cachePath := fs.String("cache-path", "", "override the shared GitHub App token cache path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		in, err := pullquery.ParseInputs(*repositories, *labels, *state, *draft)
		if err != nil {
			return err
		}
		client, err := queryClient(*watcherBin, *cachePath)
		if err != nil {
			return err
		}
		items, err := pullquery.Poll(ctx, client, in)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(items)
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
		return fmt.Errorf("unknown subcommand %q; expected observe or query-pulls", args[0])
	}
}
