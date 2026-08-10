// Command sennit-web serves the sennit session management web UI (control plane).
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kecbigmt/sennit/app/internal/webui"
)

func main() {
	host := flag.String("host", "",
		"interface to bind: 127.0.0.1 (default), a private-network/VPN IP, or 0.0.0.0 to expose. Overrides config.")
	var port string
	flag.StringVar(&port, "port", "", "port to bind. Overrides config.")
	flag.StringVar(&port, "p", "", "port to bind (shorthand).")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("component", "sennit-web")

	cfg := webui.LoadConfig()
	addr := cfg.Addr(*host, port)
	srv := webui.NewWithConfig(webui.NewLiveService(), cfg)
	server := &http.Server{Addr: addr, Handler: srv.Routes()}

	go func() {
		logger.Info("listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	logger.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
