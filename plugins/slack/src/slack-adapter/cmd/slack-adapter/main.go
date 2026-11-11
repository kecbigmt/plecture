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
		return fmt.Errorf("usage: slack-adapter <serve|resource|thread|message|status> ...")
	}
	switch args[0] {
	case "serve":
		return serve(input, output, errors)
	case "resource":
		if len(args) != 4 || args[1] != "observe" || args[2] != "--resource" || args[3] == "" {
			return fmt.Errorf("usage: slack-adapter resource observe --resource <url>")
		}
		_, err := fmt.Fprintln(output, "{}")
		return err
	case "thread", "message", "status":
		return action(args, output, errors)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve(input io.Reader, output, errors io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(errors, nil))
	protocol := adapter.NewServiceProtocol(input, output)
	config, err := protocol.ReadConfig()
	if err != nil {
		return err
	}
	bots, err := adapter.ParseServiceConfig(config)
	if err != nil {
		return err
	}
	if err := protocol.ReadSnapshot(); err != nil {
		return err
	}
	denials, err := adapter.NewDenialStore(os.Getenv("PLECT_DATA_DIR"))
	if err != nil {
		return err
	}
	listeners := make([]*adapter.BotAdapter, 0, len(bots))
	for _, bot := range bots {
		listener, err := adapter.NewBotAdapter(bot, protocol, denials, logger)
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, len(listeners)+1)
	go func() { failures <- protocol.ReadReplies() }()
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
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *configJSON == "" || *channel == "" {
		return fmt.Errorf("--config and --channel-id are required")
	}
	bots, err := adapter.ParseServiceConfig([]byte(*configJSON))
	if err != nil {
		return err
	}
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
	case "status set":
		if *thread == "" {
			return fmt.Errorf("--thread-ts is required")
		}
		return client.SetStatus(*thread, *body)
	default:
		return fmt.Errorf("unknown action %s %s", args[0], args[1])
	}
}
