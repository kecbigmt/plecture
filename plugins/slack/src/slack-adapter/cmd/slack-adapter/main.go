package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/kecbigmt/plecture/plugins/slack/src/slack-adapter/internal/adapter"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "slack-adapter:", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, errors io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: slack-adapter <serve|resource|thread|message|reply|status> ...")
	}
	switch args[0] {
	case "serve":
		return serve(input, output, errors)
	case "subscribe":
		return subscribe(args[1:], output)
	case "resource":
		if len(args) != 4 || args[1] != "observe" || args[2] != "--resource" || args[3] == "" {
			return fmt.Errorf("usage: slack-adapter resource observe --resource <url>")
		}
		_, err := fmt.Fprintln(output, "{}")
		return err
	case "thread", "message", "reply", "status":
		return action(args, output, errors)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func subscribe(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "unbound-mentions" {
		return fmt.Errorf("usage: slack-adapter subscribe unbound-mentions --socket <path> --channel-ids <json-array>")
	}
	fs := flag.NewFlagSet("subscribe unbound-mentions", flag.ContinueOnError)
	path := fs.String("socket", "", "private feed socket")
	channelsJSON := fs.String("channel-ids", "", "channel IDs")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var channels []string
	if err := json.Unmarshal([]byte(*channelsJSON), &channels); err != nil || *path == "" || len(channels) == 0 || fs.NArg() != 0 {
		return fmt.Errorf("subscribe unbound-mentions requires --socket and nonempty --channel-ids JSON array")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return adapter.StreamMentions(ctx, *path, channels, output)
}

func serve(input io.Reader, output, errors io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(errors, nil))
	protocol := adapter.NewServiceProtocol(input, output)
	config, err := protocol.ReadConfig()
	if err != nil {
		return err
	}
	serviceConfig, err := adapter.ParseServiceConfig(config)
	if err != nil {
		return err
	}
	bots := serviceConfig.Bots
	if err := protocol.ReadSnapshot(); err != nil {
		return err
	}
	coverage := adapter.NewCoverage(bots, serviceConfig.SocketPath)
	if err := protocol.ReadQueries(coverage); err != nil {
		return err
	}
	denials, err := adapter.NewDenialStore(os.Getenv("PLECT_DATA_DIR"))
	if err != nil {
		return err
	}
	recovery, err := adapter.NewRecoveryStore(os.Getenv("PLECT_DATA_DIR"))
	if err != nil {
		return err
	}
	pending, err := adapter.NewPendingStore(os.Getenv("PLECT_DATA_DIR"))
	if err != nil {
		return err
	}
	var feed *adapter.MentionFeed
	if serviceConfig.SocketPath != "" {
		feed, err = adapter.NewMentionFeed(serviceConfig.SocketPath, coverage)
		if err != nil {
			return err
		}
		defer feed.Close()
		protocol.OnQueriesChanged(feed.DropClients)
	}
	listeners := make([]*adapter.BotAdapter, 0, len(bots))
	for _, bot := range bots {
		listener, err := adapter.NewBotAdapter(bot, protocol, denials, logger)
		if err != nil {
			return err
		}
		listener.ConfigureRecovery(recovery, protocol.Subscriptions())
		listener.ConfigurePopulation(coverage, pending, feed)
		listeners = append(listeners, listener)
	}
	if err := adapter.ValidateUniqueIdentities(listeners); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, len(listeners)+2)
	go func() { failures <- protocol.ReadReplies() }()
	if feed != nil {
		go func() { failures <- feed.Serve(ctx) }()
	}
	var running sync.WaitGroup
	for _, listener := range listeners {
		running.Add(1)
		go func(b *adapter.BotAdapter) { defer running.Done(); failures <- b.Run(ctx) }(listener)
	}
	select {
	case <-ctx.Done():
		running.Wait()
		return nil
	case err := <-failures:
		stop()
		running.Wait()
		return err
	}
}

func action(args []string, output, errors io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("missing %s subcommand", args[0])
	}
	fs := flag.NewFlagSet(args[0]+" "+args[1], flag.ContinueOnError)
	fs.SetOutput(errors)
	configJSON := fs.String("config", "", "validated service config as JSON")
	channel := fs.String("channel-id", "", "Slack channel ID")
	thread := fs.String("thread-ts", "", "Slack thread timestamp")
	body := fs.String("text", "", "message text")
	eventType := fs.String("event-type", "", "channel event type")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if args[0] == "reply" && args[1] == "post" {
		switch *eventType {
		case "plect.message_delta":
			fmt.Fprintln(errors, "plect.message_delta should be removed from include")
			return nil
		case "plect.message":
		default:
			return fmt.Errorf("reply post requires --event-type plect.message or plect.message_delta")
		}
	}
	if *configJSON == "" || *channel == "" {
		return fmt.Errorf("--config and --channel-id are required")
	}
	serviceConfig, err := adapter.ParseServiceConfig([]byte(*configJSON))
	if err != nil {
		return err
	}
	bots := serviceConfig.Bots
	client, err := adapter.NewAction(bots, *channel, "https://slack.com/api/")
	if err != nil {
		return err
	}
	switch args[0] + " " + args[1] {
	case "thread create":
		if *body == "" {
			return fmt.Errorf("--text is required")
		}
		ts, link, err := client.CreateThread(*body)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]string{"thread_ts": ts, "channel_id": *channel, "permalink": link})
	case "message post":
		if *thread == "" {
			return fmt.Errorf("--thread-ts is required")
		}
		return client.PostMessage(*thread, *body)
	case "reply post":
		if *thread == "" {
			return fmt.Errorf("--thread-ts is required")
		}
		return client.PostReply(*thread, *body, errors)
	case "status set":
		if *thread == "" {
			return fmt.Errorf("--thread-ts is required")
		}
		return client.SetStatus(*thread, *body)
	default:
		return fmt.Errorf("unknown action %s %s", args[0], args[1])
	}
}
