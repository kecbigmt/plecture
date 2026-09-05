// Command github-webhook-receiver is the executable the shipped GitHub pull
// resource's query.subscribe action runs: a supervised HTTP service that
// receives GitHub webhook deliveries and streams validated pull-request
// appearances, one JSON item per line, to stdout. Exposing its endpoint and
// provisioning its signing secret are deployment-infrastructure
// responsibilities; this binary only verifies deliveries against a secret
// already present in its environment. See
// docs/adr/2026-09-05-standing-session-dispatch.md, "Poll-and-subscribe
// observer sketch".
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/pullquery"
)

// envSecret is the GitHub webhook signing secret this service verifies
// deliveries against. Read from the environment, never a flag, so it never
// appears in a process listing or in committed config.
const envSecret = "GITHUB_WEBHOOK_RECEIVER_SECRET"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "github-webhook-receiver:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: github-webhook-receiver subscribe-pulls [flags]")
	}
	switch args[0] {
	case "subscribe-pulls":
		return cmdSubscribePulls(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q; expected subscribe-pulls", args[0])
	}
}

func cmdSubscribePulls(args []string) error {
	fs := flag.NewFlagSet("subscribe-pulls", flag.ContinueOnError)
	repositories := fs.String("repositories", "[]", "JSON array of \"owner/repo\" strings to match")
	labels := fs.String("labels", "[]", "JSON array of label names every matched pull request must carry")
	state := fs.String("state", "", "\"open\", \"closed\", or \"all\"")
	draft := fs.String("draft", "", "\"true\"/\"false\": match only pull requests whose draft flag equals this")
	addr := fs.String("addr", "127.0.0.1:0", "address to receive GitHub webhook deliveries on; exposing it publicly is deployment infrastructure, not this binary's concern")
	path := fs.String("path", "/", "HTTP path GitHub delivers webhooks to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in, err := pullquery.ParseInputs(*repositories, *labels, *state, *draft)
	if err != nil {
		return err
	}
	secret := os.Getenv(envSecret)
	if secret == "" {
		return fmt.Errorf("%s is required", envSecret)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	mux := http.NewServeMux()
	mux.Handle(*path, newHandler(secret, in, out, logger))

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *addr, err)
	}
	logger.Info("github-webhook-receiver listening", "addr", listener.Addr().String(), "path", *path)

	server := &http.Server{Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case <-ctx.Done():
		return server.Close()
	case err := <-serveErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
