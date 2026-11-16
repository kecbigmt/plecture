package commands

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/datahome"
	"github.com/kecbigmt/plecture/app/internal/sockettest"
)

func TestServeSocketSelection(t *testing.T) {
	root := sockettest.Dir(t)
	runtimeDir := filepath.Join(root, "runtime")
	xdgDataDir := filepath.Join(root, "data")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv(datahome.XDGEnvVar, xdgDataDir)
	defaultSocket := filepath.Join(runtimeDir, "plect", "bus.sock")
	customSocket := filepath.Join(root, "custom.sock")
	customData := filepath.Join(root, "custom-data")
	for _, tc := range []struct {
		name, dataHome, socket, want, wantErr string
	}{
		{name: "default home", want: defaultSocket},
		{name: "override equal to default", dataHome: filepath.Join(xdgDataDir, "plect"), want: defaultSocket},
		{name: "custom home needs socket", dataHome: customData, wantErr: "non-default data home requires --socket"},
		{name: "custom home with socket", dataHome: customData, socket: customSocket, want: customSocket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(datahome.EnvVar, tc.dataHome)
			got, err := selectServeSocket(tc.socket)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("selectServeSocket() error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("selectServeSocket() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestServeCustomSocketDoesNotBindDefault(t *testing.T) {
	root := sockettest.Dir(t)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	t.Setenv(datahome.XDGEnvVar, filepath.Join(root, "data"))
	t.Setenv(datahome.EnvVar, filepath.Join(root, "custom-data"))
	path, err := selectServeSocket(filepath.Join(root, "custom.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := listenBusSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := os.Lstat(defaultBusSocket()); !os.IsNotExist(err) {
		t.Fatalf("default socket exists: %v", err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("custom socket is not listening: %v", err)
	}
	conn.Close()
}

func TestListenBusSocketRefusesLiveListener(t *testing.T) {
	path := filepath.Join(sockettest.Dir(t), "bus.sock")
	first, err := listenBusSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := listenBusSocket(path)
	if second != nil {
		second.Close()
		t.Fatal("second listener unexpectedly started")
	}
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "live listener") {
		t.Fatalf("error = %v, want path and live listener", err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("first listener's socket was disturbed: %v", err)
	}
	conn.Close()
}

func TestListenBusSocketCloseLeavesReplacementUntouched(t *testing.T) {
	path := filepath.Join(sockettest.Dir(t), "bus.sock")
	first, err := listenBusSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("replacement socket was unlinked: %v", err)
	}
	conn.Close()
}

func TestListenBusSocketCloseRemovesOwnedSocket(t *testing.T) {
	path := filepath.Join(sockettest.Dir(t), "bus.sock")
	ln, err := listenBusSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after close: %v", err)
	}
}

func TestListenBusSocketPreservesExistingPath(t *testing.T) {
	path := filepath.Join(sockettest.Dir(t), "bus.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := listenBusSocket(path)
	if ln != nil {
		ln.Close()
		t.Fatal("listener unexpectedly started")
	}
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want socket path", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing path changed: %q, %v", data, err)
	}
}

func TestListenBusSocketReclaimsStaleSocket(t *testing.T) {
	path := filepath.Join(sockettest.Dir(t), "bus.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	ln, err := listenBusSocket(path)
	if err != nil {
		t.Fatalf("stale socket was not reclaimed: %v", err)
	}
	defer ln.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("reclaimed socket is not listening: %v", err)
	}
	conn.Close()
}
