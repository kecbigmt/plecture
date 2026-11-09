package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kecbigmt/plecture/plugins/slack/src/slack-adapter/internal/adapter"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "subscribe":
			os.Exit(runSubscribeCommand(os.Args[2:], os.Stdout, os.Stderr))
		case "resource":
			os.Exit(runResourceCommand(os.Args[2:], os.Stdout, os.Stderr))
		}
	}
	flags, err := parseServerFlags(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	runServer(flags)
}

type serverFlags struct {
	configPath string
	stateDir   string
}

func parseServerFlags(args []string, errOut io.Writer) (serverFlags, error) {
	var flags serverFlags
	fs := flag.NewFlagSet("slack-adapter", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&flags.configPath, "config", "", "config file (default $SLACK_ADAPTER_CONFIG, else ~/.config/slack-adapter/config.toml)")
	fs.StringVar(&flags.stateDir, "state-dir", "", "directory for subscribers.json and streams.json (default $SLACK_ADAPTER_STATE_DIR, else $XDG_STATE_HOME/slack-adapter)")
	if err := fs.Parse(args); err != nil {
		return serverFlags{}, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errOut, "slack-adapter: unexpected argument %q\n", fs.Arg(0))
		return serverFlags{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return flags, nil
}

// runSubscribeCommand implements `slack-adapter subscribe unbound-mentions`.
// It never opens a Socket Mode connection of its own: it is a client of the
// resident adapter's /unbound-mentions feed, so any number of these can run
// without each consuming another connection to Slack.
func runSubscribeCommand(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "unbound-mentions" {
		fmt.Fprintln(errOut, "usage: slack-adapter subscribe unbound-mentions --base-url <url> --channel-ids <json-array> [--user-ids <json-array>] [--denied-user-message <text>] [--denied-channel-message <text>]")
		return 2
	}

	fs := flag.NewFlagSet("subscribe unbound-mentions", flag.ContinueOnError)
	fs.SetOutput(errOut)
	baseURL := fs.String("base-url", "", "resident slack-adapter base URL (required)")
	channelIDsJSON := fs.String("channel-ids", "[]", "JSON array of channel IDs to include")
	userIDsJSON := fs.String("user-ids", "[]", "JSON array of Slack user IDs allowed to start a session; empty allows everyone")
	deniedUserMessage := fs.String("denied-user-message", "", "reply posted once per thread and user to a mention from a user outside --user-ids; empty drops them silently")
	deniedChannelMessage := fs.String("denied-channel-message", "", "reply posted once per thread and user to a mention in a channel outside --channel-ids; empty drops them silently")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *baseURL == "" {
		fmt.Fprintln(errOut, "slack-adapter subscribe unbound-mentions: --base-url is required")
		return 2
	}
	var channelIDs []string
	if err := json.Unmarshal([]byte(*channelIDsJSON), &channelIDs); err != nil {
		fmt.Fprintf(errOut, "slack-adapter subscribe unbound-mentions: --channel-ids: %v\n", err)
		return 2
	}

	var userIDs []string
	if err := json.Unmarshal([]byte(*userIDsJSON), &userIDs); err != nil {
		fmt.Fprintf(errOut, "slack-adapter subscribe unbound-mentions: --user-ids: %v\n", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	filter := adapter.MentionFilter{ChannelIDs: channelIDs, UserIDs: userIDs, DeniedUserMessage: *deniedUserMessage, DeniedChannelMessage: *deniedChannelMessage}
	if err := adapter.RunSubscribeUnboundMentions(ctx, *baseURL, filter, out); err != nil {
		fmt.Fprintln(errOut, "slack-adapter subscribe unbound-mentions:", err)
		return 1
	}
	return 0
}

// thread has nothing live to report beyond a mention appearance, which
// already flows through query.subscribe, so this prints "{}" rather than
// fabricate a state key.
func runResourceCommand(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || (args[0] != "observe" && args[0] != "subscribe" && args[0] != "unsubscribe") {
		fmt.Fprintln(errOut, "usage: slack-adapter resource <observe|subscribe|unsubscribe> --resource <url> [--session <name>]")
		return 2
	}

	fs := flag.NewFlagSet("resource "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	resource := fs.String("resource", "", "Slack resource identifier")
	session := fs.String("session", "", "subscriber session")
	through := fs.String("catch-up-through", "", "publish history through this Slack timestamp")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *resource == "" {
		fmt.Fprintln(errOut, "slack-adapter resource observe: --resource is required")
		return 2
	}

	if args[0] == "observe" {
		fmt.Fprintln(out, "{}")
		return 0
	}
	cfg := adapter.LoadConfig("")
	var err error
	if team, bot, parseErr := adapter.ParseMentionResource(*resource); parseErr == nil {
		baseURL := adapter.MentionBaseURL(adapter.InstanceRegistryDir(), team, bot, cfg.ListenAddr)
		err = adapter.MentionSubscription(context.Background(), nil, baseURL, args[0], *session, *resource)
	} else {
		err = adapter.ThreadSubscription(context.Background(), nil, "http://"+cfg.ListenAddr, args[0], *session, *resource, *through)
	}
	if err != nil {
		fmt.Fprintln(errOut, "slack-adapter resource:", err)
		return 1
	}
	return 0
}

func runServer(flags serverFlags) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("component", "slack-adapter")

	cfg := adapter.LoadConfig(flags.configPath)

	if err := cfg.ValidateStartup(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	a, err := adapter.New(cfg, adapter.ResolveStateDir(flags.stateDir), logger)
	if err != nil {
		logger.Error("load subscriber state", "error", err)
		os.Exit(1)
	}
	defer a.Close()
	a.SweepSubscriptions(context.Background(), adapter.LiveSessions)

	mux := http.NewServeMux()
	mux.HandleFunc("/info", a.HandleInfo)
	mux.HandleFunc("/threads", a.HandleCreateThread)
	mux.HandleFunc("/messages", a.HandlePostMessage)
	mux.HandleFunc("/subscribe", a.HandleSubscribe)
	mux.HandleFunc("/subscribers", a.HandleSubscribers)
	mux.HandleFunc("/unbound-mentions", a.HandleUnboundMentions)
	mux.HandleFunc("/notify", a.HandleNotify)
	mux.HandleFunc("/status", a.HandleSetStatus)
	mux.HandleFunc("/stream", a.HandleStream)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP + WebSocket server
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Error("listen", "addr", cfg.ListenAddr, "error", err)
		os.Exit(1)
	}
	// Registering only after the address is bound keeps a hook from being
	// routed to an instance that never came up on it.
	if err := a.RegisterInstance(adapter.InstanceRegistryDir(), cfg.ListenAddr); err != nil {
		logger.Warn("instance not registered; a mentions hook for this bot falls back to its own configured listen_addr", "error", err)
	}
	server := &http.Server{Handler: mux}
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	// Start the optional Slack Socket Mode relay.
	go func() {
		if err := a.Run(ctx); err != nil {
			logger.Error("slack socket mode error", "error", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case <-sigCh:
		logger.Info("shutting down")
	case <-ctx.Done():
	}

	cancel()
	server.Close()
}
