package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
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
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

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
	sup := NewSupervisor(func() *config.Config { return cfg }, stateStore, log, hub)
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

// TestSupervisor_DeliversChannelDefinedOnlyInAPluginMountedAfterConstruction
// reproduces the bus-daemon outage class: a channel definition that exists
// only in a plugin layer (no global copy) must validate and deliver once the
// supervisor's config getter reflects the plugin, even though the getter
// returned a Config without that plugin when the Supervisor was built —
// matching a long-running daemon whose config.Live refresh mounts a plugin
// after `plect bus serve` started, not just after a restart.
func TestSupervisor_DeliversChannelDefinedOnlyInAPluginMountedAfterConstruction(t *testing.T) {
	pluginDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "channels", "claude.toml"), `
type = "unix_socket"
path = "{{.Inputs.path}}"
body = "{{ json .Event }}"

[input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(pluginDir, "workflows", "coding.toml"), `
[[event.channel]]
name        = "runtime"
uses        = "claude"
inputs.path = "{{.Nodes.claude.outputs.socket_path}}"
include     = ["plect.instruction"]
`)

	// currentCfg starts without the plugin mounted, as if the daemon started
	// before this plugin was enabled; the getter closure below always reads
	// its current value, so reassigning it simulates config.Live swapping in
	// a freshly-resolved Config on its next periodic refresh.
	currentCfg := &config.Config{}

	stateStore := state.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	if err := stateStore.Put(&domain.Session{
		Name: "o/r-1", Workflow: "coding", WorkdirPath: t.TempDir(),
		Tasks: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{"socket_path": sock}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	log := eventlog.NewStore(t.TempDir())
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	defer hub.Close()
	sup := NewSupervisor(func() *config.Config { return currentCfg }, stateStore, log, hub)
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

	// The plugin mounts (config refresh happens) before this session's
	// dispatcher is built.
	currentCfg = &config.Config{PluginDirs: []string{pluginDir}}

	sup.reconcile(ctx, active, skip, &wg)
	if _, ok := active["o/r-1"]; !ok {
		t.Fatal("dispatcher not started for an up session")
	}
	time.Sleep(50 * time.Millisecond) // let the dispatcher seed its cursor and reach Watch before the append below

	if _, _, _, err := log.Append(event.Event{SessionName: "o/r-1", Type: event.TypeInstruction, Body: "go"}); err != nil {
		t.Fatal(err)
	}
	if typ := recvType(t, recv); typ != event.TypeInstruction {
		t.Errorf("delivered type = %q, want %q (plugin-only channel definition did not resolve)", typ, event.TypeInstruction)
	}
}
