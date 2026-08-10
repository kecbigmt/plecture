package mcpserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// Listen accepts Unix-socket MCP clients and gives each connection its own
// stdio `sennit mcp serve` child process. A non-empty guard makes this socket's
// scope independent of whatever SENNIT_SESSION_GUARD the listening process
// itself inherited.
func Listen(ctx context.Context, socketPath, self, guard string) error {
	if _, err := os.Stat(socketPath); err == nil {
		os.Remove(socketPath)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", socketPath, err)
	}
	defer listener.Close()

	// Rootless Docker remaps UIDs, so filesystem visibility via the mounted
	// socket directory is the boundary; 0600 would block intended container users.
	if err := os.Chmod(socketPath, 0666); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Listening on %s\n", socketPath)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Accept error: %v\n", err)
			continue
		}

		go handleConnection(ctx, self, conn, guard)
	}
}

func handleConnection(ctx context.Context, self string, conn net.Conn, guard string) {
	defer conn.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, self, "mcp", "serve")
	cmd.Stdin = conn
	cmd.Stdout = conn
	cmd.Stderr = os.Stderr
	if guard != "" {
		cmd.Env = envWithOverride(os.Environ(), "SENNIT_SESSION_GUARD", guard)
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		}
	}
}

// envWithOverride returns base with key set to value, replacing any existing
// entry rather than appending a duplicate — duplicate keys in a child's
// envp are resolved inconsistently across libc getenv implementations, so
// relying on "last wins" via append alone is not safe.
func envWithOverride(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, prefix+value)
}
