package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// TestFlakeOverlays_ClaudeCodexCompile guards every repo-specific claude.toml /
// codex.toml overlay under flakes/github.com/*/*/plect/workflows/ against a
// `blocks` entry naming a node id that doesn't exist in the merged (global +
// overlay) graph. That only surfaces at `plect up` time for whichever repo
// someone happens to be dispatching in, so a stale overlay (e.g. a node
// renamed upstream) can sit broken for every other repo indefinitely. This
// compiles the real deploy shape: a `.plect` symlink at the repo base dir, one
// layer above the workdir, exactly as scripts/repo-clone sets it up.
func TestFlakeOverlays_ClaudeCodexCompile(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..")
	cfgDir := filepath.Join(root, "config", "plect")
	flakesHostDir := filepath.Join(root, "flakes", "github.com")
	if _, err := os.Stat(flakesHostDir); err != nil {
		t.Skipf("flakes dir not found at %s: %v", flakesHostDir, err)
	}

	owners, err := os.ReadDir(flakesHostDir)
	if err != nil {
		t.Fatalf("read %s: %v", flakesHostDir, err)
	}
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		ownerDir := filepath.Join(flakesHostDir, owner.Name())
		repos, err := os.ReadDir(ownerDir)
		if err != nil {
			t.Fatalf("read %s: %v", ownerDir, err)
		}
		for _, repo := range repos {
			if !repo.IsDir() {
				continue
			}
			plectDir := filepath.Join(ownerDir, repo.Name(), "plect")
			workflowsDir := filepath.Join(plectDir, "workflows")
			for _, id := range []string{"claude", "codex"} {
				if _, err := os.Stat(filepath.Join(workflowsDir, id+".toml")); err != nil {
					continue
				}
				t.Run(owner.Name()+"/"+repo.Name()+"/"+id, func(t *testing.T) {
					repoBase := t.TempDir()
					plectureLink := filepath.Join(repoBase, ".plect")
					if err := os.Symlink(plectDir, plectureLink); err != nil {
						t.Fatalf("symlink .plect: %v", err)
					}
					workdirDir := filepath.Join(repoBase, "workdir")

					cfg := &config.Config{BaseDir: cfgDir}
					workflows, err := cfg.LoadWorkflows(workdirDir)
					if err != nil {
						t.Fatalf("LoadWorkflows: %v", err)
					}
					wf, ok := workflows[id]
					if !ok {
						t.Fatalf("workflow %q not found after merge", id)
					}
					defs, err := cfg.LoadTaskDefinitions(workdirDir)
					if err != nil {
						t.Fatalf("LoadTaskDefinitions: %v", err)
					}
					if _, err := task.CompileWorkflow(wf, defs); err != nil {
						t.Errorf("compile workflow %q: %v", id, err)
					}
				})
			}
		}
	}
}
