package webui

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config controls how plecture-web binds and (later) authenticates. Loaded from
// ~/.config/plecture-web/config.toml; missing file falls back to defaults.
type Config struct {
	// ListenAddr defaults to loopback so the control plane is not exposed on
	// every interface by accident. To reach it from a phone over a private network/VPN,
	// set it (or pass -listen) to that network's IP, or 0.0.0.0 to bind all.
	ListenAddr string `toml:"listen_addr"`
	// AuthToken, when set, gates the whole UI: requests must carry it as an
	// Authorization: Bearer header or the login cookie (defense in depth for
	// exposing the control plane over a private network/VPN). Empty = network trust.
	AuthToken string `toml:"auth_token"`
	// BusSocket is the plecture bus server's Unix socket. The live timeline opens its
	// SSE stream over this socket and relays frames to the browser (which can
	// neither dial a UDS nor hold the bus token). Empty falls back to the
	// PLECTURE_BUS_SOCKET env var, then $XDG_RUNTIME_DIR/plecture/bus.sock.
	BusSocket string `toml:"bus_socket"`
	// BusToken authenticates to the bus when it requires one. Empty falls back to
	// the PLECTURE_BUS_TOKEN env var (same-user UDS needs none).
	BusToken string `toml:"bus_token"`
}

func LoadConfig() *Config {
	cfg := &Config{ListenAddr: "127.0.0.1:8787"}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "plecture-web", "config.toml")
		if _, statErr := os.Stat(path); statErr == nil {
			// An absent config.toml is a normal defaults-only setup, but a
			// present-and-unparsable one is a user mistake that would
			// otherwise silently fall back to defaults with no signal at all.
			if _, decodeErr := toml.DecodeFile(path, cfg); decodeErr != nil {
				slog.Warn("config.toml present but failed to parse; using defaults", "path", path, "error", decodeErr)
			}
		}
	}
	if cfg.BusSocket == "" {
		cfg.BusSocket = defaultBusSocket()
	}
	if cfg.BusToken == "" {
		cfg.BusToken = os.Getenv("PLECTURE_BUS_TOKEN")
	}
	return cfg
}

// defaultBusSocket mirrors `plecture bus serve`'s convention: the PLECTURE_BUS_SOCKET env
// var if set, else %t/plecture/bus.sock ($XDG_RUNTIME_DIR), else a tmp fallback.
func defaultBusSocket() string {
	if s := os.Getenv("PLECTURE_BUS_SOCKET"); s != "" {
		return s
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "plecture", "bus.sock")
	}
	return filepath.Join(os.TempDir(), "plecture", "bus.sock")
}

// Addr resolves the bind address from ListenAddr, applying optional host/port
// overrides (from -host / -port flags). Empty overrides keep the configured value.
func (c *Config) Addr(host, port string) string {
	h, p, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		h, p = "127.0.0.1", "8787"
	}
	if host != "" {
		h = host
	}
	if port != "" {
		p = port
	}
	return net.JoinHostPort(h, p)
}
