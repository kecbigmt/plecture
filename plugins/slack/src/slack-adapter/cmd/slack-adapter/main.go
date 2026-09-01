package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kecbigmt/plecture/plugins/slack/src/slack-adapter/internal/adapter"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("component", "slack-adapter")

	cfg := adapter.LoadConfig()

	if err := cfg.ValidateStartup(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	a := adapter.New(cfg, logger)
	defer a.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/info", a.HandleInfo)
	mux.HandleFunc("/threads", a.HandleCreateThread)
	mux.HandleFunc("/messages", a.HandlePostMessage)
	mux.HandleFunc("/subscribe", a.HandleSubscribe)
	mux.HandleFunc("/subscribers", a.HandleSubscribers)
	mux.HandleFunc("/notify", a.HandleNotify)
	mux.HandleFunc("/status", a.HandleSetStatus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP + WebSocket server
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
