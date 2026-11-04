package task

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/procid"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

var nestingExampleRe = regexp.MustCompile("(?s)<!-- fixture: nesting/(\\S+)\\.toml -->\n```toml\n(.*?)```")

// The nesting example the language reference shows is the shape users copy,
// so it is loaded and planned against the shipped runtime it names; a
// declaration the loader or planner rejects would otherwise only fail for the
// reader who followed the documentation.
func TestDocumentedNestingExamples_LoadPlanAndJudgeTheShippedRuntimeThroughTheChain(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRootForTest(t), "docs", "language", "effects.md"))
	if err != nil {
		t.Fatal(err)
	}
	examples := nestingExampleRe.FindAllSubmatch(raw, -1)
	if len(examples) == 0 {
		t.Fatal("docs/language/effects.md carries no nesting example")
	}
	for _, m := range examples {
		name, body := string(m[1]), m[2]
		t.Run(name, func(t *testing.T) {
			cfg := shippedCatalogConfig(t)
			cfg.BaseDir = t.TempDir()
			if err := os.MkdirAll(filepath.Join(cfg.BaseDir, "tasks"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", "example.toml"), body, 0o644); err != nil {
				t.Fatal(err)
			}
			defs, err := cfg.LoadTaskDefinitions("")
			if err != nil {
				t.Fatalf("LoadTaskDefinitions: %v", err)
			}
			var def config.TaskDefinition
			for id, d := range defs {
				if d.IsNested() && !strings.HasPrefix(id, "official") {
					def = d
				}
			}
			if def.ID == "" {
				t.Fatal("the example declares no nested definition")
			}
			plan, err := CompileWorkflow(
				config.WorkflowFile{ID: "test", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: def.ID}}},
				map[string]config.TaskDefinition{def.ID: def},
			)
			if err != nil {
				t.Fatalf("CompileWorkflow: %v", err)
			}
			judgeThroughTheChain(t, plan.Run[0], cfg.Plugins)
		})
	}
}

func judgeThroughTheChain(t *testing.T, r Resolved, mounted []plugins.Mounted) {
	t.Helper()
	if effect.ProcessLayer(r.Layers) < 0 {
		t.Fatal("the shipped runtime this example nests declares a process probe, and the planned chain lost it")
	}
	self := os.Getpid()
	identity, err := procid.Capture(self)
	if err != nil {
		t.Fatal(err)
	}
	reused := identity
	reused.Start += 1000
	layers := []contract.LayerState{
		{EffectID: r.Layers[0].EffectID, Status: contract.TaskStatusProduced, Locals: map[string]any{"guard_dir": "/tmp/g"}},
		{EffectID: r.Layers[1].EffectID, Status: contract.TaskStatusProduced, Outputs: map[string]any{"pid": self, "socket_path": "/tmp/s"}},
	}
	for name, tc := range map[string]struct {
		process *contract.ProcessIdentity
		wantErr string
	}{
		"the recorded process passes": {&identity, ""},
		"a reused pid fails":          {&reused, "different process"},
	} {
		t.Run(name, func(t *testing.T) {
			existing := &contract.TaskState{Status: contract.TaskStatusProduced, Layers: layers, Process: tc.process}
			err := verifyLiveness(context.Background(), r, SessionVars{Name: "s", Plugins: mounted}, existing)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyLiveness: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifyLiveness error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
