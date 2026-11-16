package reactor

import (
	"context"
	"path/filepath"
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

func TestSupervisor_SleepsAfterDurableIdleDeadline(t *testing.T) {
	pluginDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "tasks", "runner.toml"), "[runner]\nkind = \"effect\"\nscope = \"run\"\n")
	writeFile(t, filepath.Join(pluginDir, "config", "workflows", "worker.toml"), "[worker]\nkind = \"workflow\"\nresource = \"issue\"\n[[worker.nodes]]\nuses = \"runner\"\n[worker.session]\nsleep_after_idle = \"10ms\"\n")
	cfg := &config.Config{PluginDirs: []string{pluginDir}}
	store := state.NewStore(t.TempDir())
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "parent", Status: contract.SessionStatusDown, CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	s := &domain.Session{Name: "worker-1", Workflow: "worker", ParentSession: "parent", CreatedAt: now.Add(-time.Hour), Status: contract.SessionStatusUp,
		Nodes: map[string]*contract.TaskState{"runner": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced}},
	}
	pinRevision(t, cfg, s)
	if err := store.Put(s); err != nil {
		t.Fatal(err)
	}
	log := eventlog.NewStore(store.Dir())
	if _, _, _, err := log.Append(event.Event{SessionName: s.Name, Type: event.TypeStatusMessage, Direction: event.Outbound, Time: now.Add(-time.Minute), Metadata: map[string]string{"cleared": "true"}}); err != nil {
		t.Fatal(err)
	}
	hub := sessionhub.NewRegistry(log)
	defer hub.Close()
	sup := NewSupervisor(func() *config.Config { return cfg }, store, log, hub)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := store.Get(s.Name)
		if got.Status == contract.SessionStatusDown && !got.SleepSince.IsZero() && !cfg.RunScopeUp(got) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("supervisor did not sleep idle session: %+v", store.Get(s.Name))
}
