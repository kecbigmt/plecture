package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/mcpserver"
	"github.com/kecbigmt/plect/app/internal/service"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server commands",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server on stdio (stdin/stdout)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mcpserver.NewServer()
		return server.ServeStdio(s)
	},
}

var (
	mcpListenSocket  string
	mcpListenSession string
)

var mcpListenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen on a Unix socket, spawning a stdio MCP session per connection",
	RunE: func(cmd *cobra.Command, args []string) error {
		// --session injects PLECT_SESSION_GUARD into every spawned connection
		// rather than relying on whatever the listener process itself
		// inherited, and derives --socket's default so a task setup script
		// needn't compute one itself.
		var guard string
		socketPath := mcpListenSocket
		if mcpListenSession != "" {
			g, err := service.SessionGuardForOwnSession(mcpListenSession)
			if err != nil {
				return err
			}
			guard = g
			if socketPath == "" {
				socketPath = defaultSessionMcpListenSocket(mcpListenSession)
			}
		}
		if socketPath == "" {
			return fmt.Errorf("--socket is required (or pass --session to derive the per-session default)")
		}

		if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
			return fmt.Errorf("failed to create socket directory: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle shutdown signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-sigChan
			fmt.Fprintf(os.Stderr, "Shutting down...\n")
			cancel()
		}()

		// Find our own executable path for spawning child processes
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find own executable: %w", err)
		}

		return mcpserver.Listen(ctx, socketPath, self, guard)
	},
}

// defaultSessionMcpListenSocket mirrors channel-server's
// $XDG_RUNTIME_DIR/claude-channel/<sid>.sock convention. sessionName often
// contains "/" (e.g. "session-123"); filepath.Join turns that into nested
// directories rather than a flat filename, so the name is encoded first.
func defaultSessionMcpListenSocket(sessionName string) string {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = os.TempDir()
	}
	return filepath.Join(rt, "plect-mcp", sessionName+".sock")
}

func init() {
	mcpListenCmd.Flags().StringVar(&mcpListenSocket, "socket", "", "Unix socket path to listen on (default: the --session convention path)")
	mcpListenCmd.Flags().StringVar(&mcpListenSession, "session", "", "Scope this socket to a session: injects PLECT_SESSION_GUARD for it and its own name-space into every spawned connection, and derives --socket's default")

	mcpCmd.AddCommand(mcpServeCmd)
	mcpCmd.AddCommand(mcpListenCmd)
	rootCmd.AddCommand(mcpCmd)
}
