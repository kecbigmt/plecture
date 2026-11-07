package service

import (
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
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	noopID := id + "_noop"
	noopDoc := effectFixtureDoc(taskFixture{id: noopID, scope: "session", setup: "echo '{}'"})
	if err := os.WriteFile(filepath.Join(tasksDir, noopID+".toml"), []byte(noopDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	addFixtureWorkflow(t, cfg, id, "issue", noopID, "")
	attachWorkdir(t, cfg, id, taskFixture{id: id + "_dir", setup: mkdirScript(workspaceDir)})
}

func TestTickSession_ChainAcceptsCapacitySleepAndWakesWhenSlotFrees(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "wf",
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
