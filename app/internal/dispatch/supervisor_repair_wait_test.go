package dispatch

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestSupervisor_WithRepairWaitHoldsFailedDelivery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".config", "plect")
	writeFile(t, filepath.Join(root, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(root, "channels", "socket_channel.toml"), `
[socket_channel]
kind = "channel"
type = "unix_socket"
path = { from = "inputs.path" }
body = { json = { from = "event" } }

[socket_channel.input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(root, "tasks", "runtime.toml"), `
[runtime]
kind  = "effect"
scope = "run"
`)
	writeFile(t, filepath.Join(root, "workflows", "standing.toml"), `
[standing]
kind = "workflow"
[[standing.nodes]]
id   = "runtime"
uses = "runtime"
[[standing.event.channel]]
name        = "runtime"
uses        = "socket_channel"
inputs.path = { from = "nodes.runtime.outputs.socket_path" }
include     = ["plect.instruction"]
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.NewStore(t.TempDir())
	stateStore := state.NewStore(log.Dir())
	dead := filepath.Join(t.TempDir(), "absent.sock")
	s := &domain.Session{
		Name: "session-1", Workflow: "standing", WorkspaceDirPath: t.TempDir(),
		Nodes: map[string]*contract.TaskState{
			"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{"socket_path": dead}},
		},
	}
	putRevisionSession(t, cfg, stateStore, s)
	var asked []string
	hub := sessionhub.NewRegistry(log)
	defer hub.Close()
	sup := NewSupervisor(func() *config.Config { return cfg }, stateStore, log, hub,
		WithRepairWait(func(got *config.Config, st *state.Store, session string) (bool, error) {
			if got != cfg || st != stateStore {
				t.Errorf("repair wait got a different config or store than the supervisor's")
			}
			asked = append(asked, session)
			return true, nil
		}))
	d, _ := sup.buildDispatcher("session-1", s)
	if d == nil {
		t.Fatal("no dispatcher built")
	}
	d.policy = channel.RetryPolicy{MaxAttempts: 1, Timeout: 200 * time.Millisecond}

	log.Append(event.Event{SessionName: "session-1", Type: event.TypeInstruction, Direction: event.Internal})
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 0 {
		t.Fatalf("channel errors = %+v, want the failed delivery held", errs)
	}
	if len(asked) != 1 || asked[0] != "session-1" {
		t.Fatalf("repair wait asked about %v, want the dispatcher's session once", asked)
	}
}
