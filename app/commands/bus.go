package commands

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/dispatch"
	"github.com/kecbigmt/plecture/app/internal/eventbus"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/reactor"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var busSocket string

var busCmd = &cobra.Command{
	Use:   "bus",
	Short: "Event bus server",
}

var busServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the event bus server (HTTP/SSE fan-out over the per-session event log)",
	Long: `Serve the durable event log over a Unix domain socket as an HTTP/SSE API:
POST /v1/events (append), GET /v1/events (list), GET /v1/stream (SSE replay+live).

The socket is created 0600, so same-user processes need no token; set
PLECTURE_BUS_TOKEN to also require a bearer token (e.g. when proxied to a browser).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		socket := busSocket
		if socket == "" {
			socket = defaultBusSocket()
		}
		if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
			return fmt.Errorf("create socket dir: %w", err)
		}
		// A stale socket from a previous run would make Listen fail with EADDRINUSE.
		_ = os.Remove(socket)
		ln, err := net.Listen("unix", socket)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", socket, err)
		}
		defer func() { ln.Close(); os.Remove(socket) }()
		if err := os.Chmod(socket, 0o600); err != nil {
			return fmt.Errorf("chmod socket: %w", err)
		}

		// Resolve the log dir the same way writers do (eventlog.NewStore("") =>
		// $XDG_DATA_HOME/plecture/events, matching service.EventPublish). Log it so a
		// daemon/writer env mismatch (different XDG_DATA_HOME) is visible rather
		// than silently appending to a different tree.
		store := eventlog.NewStore("")
		// One per-session reader shared by SSE subscribers (and, with the channel
		// dispatcher onto it, the single follow loop per session).
		hub := sessionhub.NewRegistry(store)
		srv := eventbus.New(store, os.Getenv("PLECTURE_BUS_TOKEN"), hub)
		httpSrv := &http.Server{
			Handler:           srv.Routes(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       0, // SSE connections are intentionally long-lived
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		cfg := config.Load()
		stateStore := state.NewStore("")

		// Session dispatchers share this process's event log and session state:
		// one per active session delivers events to its workflow-declared channels.
		sup := dispatch.NewSupervisor(cfg, stateStore, store, hub)
		var supWG sync.WaitGroup
		supWG.Go(func() { sup.Run(ctx) })

		// The tick reactor is dispatch's sibling: one per active session,
		// ticking on a declared `[tick]` pattern, the judge builtin, or a
		// `heartbeat` sweep (docs/wiki/verification-gate.md), instead of
		// leaving `plecture tick` to an orchestrator's judgment or memory.
		react := reactor.NewSupervisor(cfg, stateStore, store, hub)
		var reactWG sync.WaitGroup
		reactWG.Go(func() { react.Run(ctx) })

		go func() {
			<-ctx.Done()
			_ = httpSrv.Close()
		}()

		fmt.Fprintf(cmd.ErrOrStderr(), "plecture bus serving on %s (events: %s)\n", socket, store.Root())
		serveErr := httpSrv.Serve(ln)
		stop()         // cancel ctx so the supervisors tear down even if Serve failed without a signal
		supWG.Wait()   // let the dispatch supervisor cancel and join its dispatchers
		reactWG.Wait() // let the reactor supervisor cancel and join its reactors
		hub.Close()    // cancel any reader still alive after subscribers/dispatchers/reactors left
		if serveErr != nil && serveErr != http.ErrServerClosed {
			return serveErr
		}
		return nil
	},
}

// defaultBusSocket mirrors the plecture-mcp convention (%t/plecture/... = $XDG_RUNTIME_DIR).
func defaultBusSocket() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "plecture", "bus.sock")
	}
	return filepath.Join(os.TempDir(), "plecture", "bus.sock")
}

func init() {
	busServeCmd.Flags().StringVar(&busSocket, "socket", "", "Unix socket path (default $XDG_RUNTIME_DIR/plecture/bus.sock)")
	busCmd.AddCommand(busServeCmd)
	rootCmd.AddCommand(busCmd)
}
