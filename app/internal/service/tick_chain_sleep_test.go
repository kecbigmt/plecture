package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const capReviewChainFixture = `
[[chains]]
id       = "review"
workflow = "reviewer_wf"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`

func writeSpawnableWorkflowFile(t *testing.T, cfg *config.Config, id, workspaceDir string) {
	t.Helper()
	tasksDir := filepath.Join(cfg.BaseDir, "tasks")
	workflowsDir := filepath.Join(cfg.BaseDir, "workflows")
	providersDir := filepath.Join(cfg.BaseDir, "workspaces")
	for _, dir := range []string{tasksDir, workflowsDir, providersDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	noopID := id + "_noop"
	noopDoc := effectFixtureDoc(taskFixture{id: noopID, scope: "session", setup: "echo '{}'"})
	if err := os.WriteFile(filepath.Join(tasksDir, noopID+".toml"), []byte(noopDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	provID, prov := providerAside(id, providerCreatingWorkspace(id, workspaceDir))
	wf := fmt.Sprintf("[%s]\nkind = \"workflow\"\nworkspace_provider = %q\n\n[[%s.nodes]]\nuses = %q\n", id, provID, id, noopID)
	if err := os.WriteFile(filepath.Join(workflowsDir, id+".toml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providersDir, provID+".toml"), []byte(prov), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTickSession_ChainAcceptsCapacitySleepAndWakesWhenSlotFrees(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(capReviewChainFixture)},
		[]nodeFixture{{id: "work"}})
	writeSpawnableWorkflowFile(t, cfg, "reviewer_wf", filepath.Join(t.TempDir(), "reviewer-wd"))
	writeCapWorkflow(t, cfg.BaseDir, "parent_wf", intPtr(1))
	seedSessionWithNodes(t, store, "parent1", "acct", 1, "parent_wf", nil)
	seedSessionWithNodes(t, store, "sibling", "acct", 2, "", upTasks())
	setParent(t, store, "sibling", "parent1")
	seedReviewWork(t, store, "work1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})
	setParent(t, store, "work1", "parent1")

	res, err := TickSession(cfg, store, TickParams{SessionName: "work1", SkipRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := findSpawn(res.Chains, "review")
	if !ok || !sp.Fired || !sp.Spawned || !sp.Sleeping {
		t.Fatalf("chain result = %+v, want accepted sleep", sp)
	}
	if got := store.Get(sp.TargetSession); got == nil || got.SleepSince.IsZero() || got.Status != contract.SessionStatusDown {
		t.Fatalf("target = %+v, want queued sleep", got)
	}
	if err := store.Update("sibling", func(s *domain.Session) error {
		s.Nodes["run_node"].Status = contract.TaskStatusCleaned
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := WakeQueuedSessions(cfg, store); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(sp.TargetSession); got.Status != contract.SessionStatusUp || !got.SleepSince.IsZero() {
		t.Fatalf("target = %+v, want awake", got)
	}
}
