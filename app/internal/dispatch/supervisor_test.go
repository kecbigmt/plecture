package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_StartsAndStopsWithRunScope(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "channels", "claude_channel.toml"), `
type = "unix_socket"
path = "{{.Inputs.path}}"
body = "{{ json .Event }}"

[input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[[event.channel]]
name        = "runtime"
uses        = "claude_channel"
inputs.path = "{{.Nodes.claude.outputs.socket_path}}"
include     = ["plect.instruction"]
`)
	cfg := config.Load()

	stateStore := state.NewStore(t.TempDir())
	sock, _ := startFakeSocket(t)
	if err := stateStore.Put(&domain.Session{
		Name: "o/r-1", Workflow: "coding", WorkdirPath: t.TempDir(),
		Tasks: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{"socket_path": sock}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	log := eventlog.NewStore(t.TempDir())
	hub := sessionhub.NewRegistry(log)
	defer hub.Close()
	sup := NewSupervisor(cfg, stateStore, log, hub)
	ctx := t.Context()
	active := map[string]context.CancelFunc{}
	skip := map[string]bool{}
	var wg sync.WaitGroup
	defer wg.Wait()
	defer func() {
		for _, c := range active {
			c()
		}
	}()

	sup.reconcile(ctx, active, skip, &wg)
	if _, ok := active["o/r-1"]; !ok {
		t.Fatal("dispatcher not started for an up session")
	}
	sup.reconcile(ctx, active, skip, &wg) // idempotent: no duplicate
	if len(active) != 1 {
		t.Fatalf("expected exactly one dispatcher, got %d", len(active))
	}

	// Run scope goes down → supervisor cancels (suspend, not teardown).
	if err := stateStore.Update("o/r-1", func(s *domain.Session) error {
		s.Tasks["claude"].Status = contract.TaskStatusCleaned
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sup.reconcile(ctx, active, skip, &wg)
	if len(active) != 0 {
		t.Fatalf("dispatcher not stopped after run scope down: %d active", len(active))
	}
}
