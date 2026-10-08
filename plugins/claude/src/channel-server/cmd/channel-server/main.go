package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
	channelserver "github.com/kecbigmt/plecture/plugins/claude/src/channel-server/server"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "send" {
		if err := runSend(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "queue" {
		if err := runQueue(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	runServe()
}

func runQueue(args []string) error {
	var socketPath, queueDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			if i+1 < len(args) {
				socketPath = args[i+1]
				i++
			}
		case "--queue-dir":
			if i+1 < len(args) {
				queueDir = args[i+1]
				i++
			}
		}
	}
	if socketPath == "" || queueDir == "" {
		return fmt.Errorf("usage: channel-server queue --socket <path> --queue-dir <dir>")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("component", "channel-server", "mode", "queue")
	listener, err := channelserver.NewQueueBridge(socketPath, queueDir, logger)
	if err != nil {
		return err
	}
	// Close on TERM rather than dying with the default disposition, so the
	// socket file is removed and a relaunch never dials a dead listener.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sigs
		listener.Close()
	}()
	logger.Info("listening", "socket_path", socketPath, "queue_dir", queueDir)
	listener.Serve()
	return nil
}

func runServe() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("component", "channel-server")

	socketPath := os.Getenv("CHANNEL_SOCKET_PATH")
	if socketPath == "" {
		logger.Error("CHANNEL_SOCKET_PATH must be set")
		os.Exit(1)
	}

	senderStore := channelserver.NewConnSenderStore()
	cs := channelserver.NewChannelServer(senderStore)

	listener, err := channelserver.NewSocketListener(socketPath, func(env protocol.Envelope, conn net.Conn) {
		switch env.Type {
		case protocol.MsgRegister:
			var reg protocol.RegisterPayload
			if err := env.UnmarshalPayload(&reg); err != nil {
				logger.Error("failed to unmarshal register", "error", err)
				senderStore.SetConn(conn, "")
			} else {
				senderStore.SetConn(conn, reg.ThreadTS)
			}
		case protocol.MsgMessage:
			var msg protocol.MessagePayload
			if err := env.UnmarshalPayload(&msg); err != nil {
				logger.Error("failed to unmarshal message", "error", err)
				return
			}
			cs.OnMessage(msg)
		}
	}, logger)
	if err != nil {
		logger.Error("failed to start socket listener", "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	go listener.Serve()
	logger.Info("listening", "socket_path", socketPath)

	if err := server.ServeStdio(cs.MCPServer()); err != nil {
		logger.Error("mcp server error", "error", err)
		os.Exit(1)
	}
}

func runSend(args []string) error {
	var socketPath, text string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			if i+1 < len(args) {
				socketPath = args[i+1]
				i++
			}
		case "--text":
			if i+1 < len(args) {
				text = args[i+1]
				i++
			}
		}
	}

	if socketPath == "" || text == "" {
		return fmt.Errorf("usage: channel-server send --socket <path> --text <message>")
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", socketPath, err)
	}
	defer conn.Close()

	data, err := protocol.NewEnvelope(protocol.MsgMessage, protocol.MessagePayload{
		Text: text,
	})
	if err != nil {
		return fmt.Errorf("failed to create envelope: %w", err)
	}

	if err := channelserver.WriteMessageTo(conn, data); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}
