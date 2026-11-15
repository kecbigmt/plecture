package commands

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/sockettest"
)

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
