// github-watcher is the resident GitHub watcher plugin for sennit.
//
// Tasks subscribe sessions (`github-watcher subscribe`) and unsubscribe on
// cleanup; the daemon (`github-watcher serve`) polls subscribed resources via
// the gh CLI and forwards change notifications through the configured delivery
// path.
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

	"github.com/cradel-dev/cradel/contracts/event"
	"github.com/cradel-dev/cradel/plugins/github-watcher/internal/ratebudget"
	"github.com/cradel-dev/cradel/plugins/github-watcher/internal/watcher"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "subscribe":
		err = cmdSubscribe(os.Args[2:])
	case "unsubscribe":
		err = cmdUnsubscribe(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
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
  github-watcher subscribe --session <name> --resource <id> [--branch <branch>]
  github-watcher unsubscribe --session <name> [--resource <id>]
  github-watcher list
  github-watcher serve [--interval 60s] [--allow-legacy-notify [--notify-url http://127.0.0.1:7890/notify]]
  github-watcher gh-api [--data-dir <dir>] <gh api args...>`)
}

func cmdSubscribe(args []string) error {
	fs := flag.NewFlagSet("subscribe", flag.ExitOnError)
	session := fs.String("session", "", "sennit session name (required)")
	resource := fs.String("resource", "", "resource identifier (required)")
	branch := fs.String("branch", "", "session branch (optional; linked-PR discovery for issues)")
	dataDir := fs.String("data-dir", "", "override data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" || *resource == "" {
		return fmt.Errorf("--session and --resource are required")
	}
	return watcher.NewStore(*dataDir).Subscribe(watcher.Subscription{
		SessionName: *session,
		Resource:    *resource,
		Branch:      *branch,
	})
}

func cmdUnsubscribe(args []string) error {
	fs := flag.NewFlagSet("unsubscribe", flag.ExitOnError)
	session := fs.String("session", "", "sennit session name (required)")
	resource := fs.String("resource", "", "resource identifier (optional; removes just this one, else all of the session's)")
	dataDir := fs.String("data-dir", "", "override data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("--session is required")
	}
	if *resource != "" {
		return watcher.NewStore(*dataDir).UnsubscribeResource(*session, *resource)
	}
	return watcher.NewStore(*dataDir).Unsubscribe(*session)
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	subs, err := watcher.NewStore(*dataDir).All()
	if err != nil {
		return err
	}
	for name, sub := range subs {
		fmt.Printf("%s\t%s\n", name, sub.Resource)
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	interval := fs.String("interval", "60s", "polling interval (min 10s)")
	notifyURL := fs.String("notify-url", "http://127.0.0.1:7890/notify", "slack-adapter notify endpoint for the legacy push path (requires --allow-legacy-notify)")
	allowLegacy := fs.Bool("allow-legacy-notify", false, "opt into the deprecated POST /notify delivery when SENNIT_BUS_SOCKET is unset (dual-run rollback)")
	dataDir := fs.String("data-dir", "", "override data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tick, err := watcher.ParseInterval(*interval)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	poller := &watcher.Poller{
		Store:  watcher.NewStore(*dataDir),
		Logger: logger,
		// Same data-dir resolution as `gh-api`, so poll and config-layer gh
		// calls back off against one shared budget.
		Guard: ratebudget.NewGuard(ghAPIDataDir(*dataDir)),
	}

	busSocket := os.Getenv("SENNIT_BUS_SOCKET")
	delivery, err := configureDelivery(poller, busSocket, os.Getenv("SENNIT_BUS_TOKEN"), *notifyURL, *allowLegacy)
	if err != nil {
		return err
	}
	switch delivery {
	case "bus":
		logger.Info("github-watcher serving", "interval", tick.String(), "delivery", "bus", "bus_socket", busSocket)
	case "notify":
		logger.Warn("github-watcher serving in DEPRECATED legacy push mode (set SENNIT_BUS_SOCKET to use the event bus)",
			"interval", tick.String(), "delivery", "notify", "notify_url", *notifyURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Establish baselines promptly on start, then tick.
	poller.Tick()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return nil
		case <-ticker.C:
			poller.Tick()
		}
	}
}

func configureDelivery(poller *watcher.Poller, busSocket, busToken, notifyURL string, allowLegacy bool) (string, error) {
	if busSocket != "" {
		poller.Bus = event.NewUDSClient(busSocket, busToken)
		return "bus", nil
	}
	if allowLegacy {
		poller.NotifyURL = notifyURL
		return "notify", nil
	}
	return "", fmt.Errorf("SENNIT_BUS_SOCKET is unset: set it to use the event bus, or pass --allow-legacy-notify to opt into the deprecated POST /notify path")
}
