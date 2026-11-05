package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestWorkflowRebaseAllFiltersAndContinuesAfterFailure(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("PLECT_CONFIG_HOME", configHome)
	t.Setenv("PLECT_DATA_HOME", t.TempDir())
	t.Setenv("PLECT_SESSION_NAME", "")
	t.Setenv("PLECT_SESSION_GUARD", "")
	t.Cleanup(func() {
		workflowRebaseAll = false
		workflowRebaseWorkflow = ""
		workflowRebaseDryRun = false
		workflowRebaseWithRestart = false
	})
	for path, body := range map[string]string{
		"config.toml":          "schema_version = 2\n",
		"workflows/work.toml":  "[work]\nkind = \"workflow\"\nresource = \"issue\"\n[[work.nodes]]\nuses = \"runtime\"\n",
		"workflows/other.toml": "[other]\nkind = \"workflow\"\nresource = \"issue\"\n",
		"tasks/runtime.toml":   "[runtime]\nkind = \"effect\"\nscope = \"run\"\n[runtime.setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n[runtime.health.alive]\ntype = \"shell\"\nscript = \"true\"\n",
	} {
		full := filepath.Join(configHome, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore("")
	put := func(name, workflow string) string {
		t.Helper()
		revision, err := cfg.CaptureWorkflowRevision("", workflow)
		if err != nil {
			t.Fatal(err)
		}
		body, digest, err := revision.CanonicalBody()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := store.Put(&domain.Session{Name: name, Workflow: workflow, Status: contract.SessionStatusDown, WorkflowRevision: digest, WorkflowRevisionBody: string(body), CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	old := put("a-needs-restart", "work")
	put("z-other", "other")
	path := filepath.Join(configHome, "tasks", "runtime.toml")
	if err := os.WriteFile(path, []byte("[runtime]\nkind = \"effect\"\nscope = \"run\"\n[runtime.setup]\ntype = \"shell\"\nscript = \"echo 1\"\n[runtime.health.alive]\ntype = \"shell\"\nscript = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	put("b-compatible", "work")
	out, err := execRoot(t, "workflow", "rebase", "--all", "--workflow", "work")
	if err == nil || !strings.Contains(err.Error(), "a-needs-restart") {
		t.Fatalf("batch error = %v; output = %q", err, out)
	}
	if !strings.Contains(out, "a-needs-restart:") || !strings.Contains(out, "b-compatible:") || strings.Contains(out, "z-other:") {
		t.Fatalf("batch selection/output = %q", out)
	}
	if store.Get("a-needs-restart").WorkflowRevision != old {
		t.Fatal("failed session revision changed")
	}
	events, _, _, err := eventlog.NewStore(store.Dir()).List("b-compatible", 0, event.Filter{Types: []string{"plect.session.rebase"}})
	if err != nil || len(events) != 1 || events[0].Metadata["result"] != "succeeded" {
		t.Fatalf("later session was skipped: events = %+v, err = %v, output = %q", events, err, out)
	}
}
