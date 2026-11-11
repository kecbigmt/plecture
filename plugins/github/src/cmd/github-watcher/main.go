// github-watcher is the resident GitHub watcher plugin for plect.
//
// The supervised service polls core subscriptions and publishes changes over stdio.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ratebudget"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/watcher"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "gh-api":
		err = cmdGhAPI(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "github-watcher: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  github-watcher serve [--interval 60s]
  github-watcher gh-api <gh api args...>

serve and gh-api both authenticate as a single GitHub App installation
instead of the operator's own "gh auth login" when GITHUB_WATCHER_APP_ID is
set (fails startup/the call loudly if the rest of the App auth env is
missing or the mint fails); unset, both use ambient "gh auth" unchanged:
  GITHUB_WATCHER_APP_ID               GitHub App id
  GITHUB_WATCHER_APP_INSTALLATION_ID  installation id (or set OWNER+REPO)
  GITHUB_WATCHER_APP_OWNER            repository owner, resolves the installation id
  GITHUB_WATCHER_APP_REPO             repository name, resolves the installation id
  GITHUB_WATCHER_APP_PRIVATE_KEY_PATH path to the App's PEM private key
  GITHUB_WATCHER_APP_BASE_URL         GitHub API base URL (default api.github.com)
  PLECT_CACHE_DIR                      plugin cache containing token and rate-budget files`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	interval := fs.String("interval", "60s", "polling interval (min 10s)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tick, err := watcher.ParseInterval(*interval)
	if err != nil {
		return err
	}
	dataDir := os.Getenv("PLECT_DATA_DIR")
	cacheDir := os.Getenv("PLECT_CACHE_DIR")
	if dataDir == "" || cacheDir == "" {
		return fmt.Errorf("PLECT_DATA_DIR and PLECT_CACHE_DIR are required for a supervised service")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store := watcher.NewProtocolStore(dataDir, cacheDir)
	protocol := newServiceProtocol(os.Stdin, os.Stdout, store)
	poller := &watcher.Poller{Store: store, Logger: logger, Guard: ratebudget.NewGuard(cacheDir)}
	poller.PublishResource = func(_, resource, eventType, summary string, metadata map[string]string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return protocol.Publish(ctx, resource, eventType, summary, metadata)
	}
	if err := configureAppAuth(poller, cacheDir); err != nil {
		return fmt.Errorf("github app auth: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	readDone := make(chan error, 1)
	go func() { readDone <- protocol.Run() }()
	select {
	case <-protocol.ready:
	case err := <-readDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	poller.Tick()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readDone:
			return err
		case <-ticker.C:
			poller.Tick()
		}
	}
}
