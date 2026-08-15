package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Status's "work" layer must carry the same decision-making material the
// retired `plect check` used to report per instance: the done_when evaluation,
// the heartbeat budget, the classified action, and the chain plan for that same
// instance.
func TestStatus_WorkCarriesActionHeartbeatBudgetAndChains(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [
  { judge_pending = "ac-met" },
  { check = "checks_status", in = ["SUCCESS"] },
]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`)},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	result, err := Status(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(result.Work) != 1 {
		t.Fatalf("len(Work) = %d, want 1", len(result.Work))
	}
	w := result.Work[0]
	if w.Instance != "work" {
		t.Fatalf("Instance = %q, want work", w.Instance)
	}
	if w.DoneWhen == nil || w.DoneWhen.Overall != task.DonePending {
		t.Fatalf("DoneWhen.Overall = %+v, want pending (checks_status satisfied, judge not yet recorded)", w.DoneWhen)
	}
	if w.Action != "review_required" {
		t.Errorf("Action = %q, want review_required", w.Action)
	}
	if w.ReviewerCommand == "" {
		t.Error("expected a non-empty ReviewerCommand for review_required")
	}
	if w.HeartbeatBudget == 0 {
		t.Error("expected heartbeat budget to be reported")
	}
	if len(w.Chains) != 1 || w.Chains[0].ChainID != "review" {
		t.Fatalf("Chains = %+v, want one entry for chain \"review\"", w.Chains)
	}
	if !w.Chains[0].Fired {
		t.Errorf("expected chain \"review\" to be fired, got %+v", w.Chains[0])
	}
	if w.Outputs["checks_status"] != "SUCCESS" {
		t.Errorf("Outputs[checks_status] = %v, want SUCCESS", w.Outputs["checks_status"])
	}
}

func TestStatus_RuntimeCarriesHealthMovementTimestamps(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "true")
	lastCheckedAt := time.Now().Add(-time.Minute).UTC()
	lastMovementAt := time.Now().Add(-2 * time.Minute).UTC()
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Health = &contract.HealthState{LastCheckedAt: lastCheckedAt, LastMovementAt: lastMovementAt}
		return nil
	}); err != nil {
		t.Fatalf("seed health state: %v", err)
	}

	result, err := Status(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if result.Runtime.LastCheckedAt.IsZero() || result.Runtime.LastMovementAt.IsZero() {
		t.Fatalf("runtime = %+v, want health timestamps", result.Runtime)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), "last_movement_at") || strings.Contains(string(b), "last_progress_at") {
		t.Fatalf("status JSON = %s, want last_movement_at only", b)
	}
}

// Summarize is the default `plect status --json` projection: only instances
// with a done_when, and only the leaf/chain fields an orchestrator needs —
// never the instance's full (unfiltered) outputs map.
func TestSummarize_FiltersToDoneWhenInstancesAndOmitsRawOutputs(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{
			workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [
  { judge_pending = "ac-met" },
  { check = "checks_status", in = ["SUCCESS"] },
]
`),
			{id: "envfile", scope: "run", setup: "echo '{}'"},
		},
		[]nodeFixture{{id: "work"}, {id: "envfile"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedReviewWork(t, store, "owner/repo-1", map[string]any{
		"checks_status": "SUCCESS",
		"revision":      "sha1",
		"instruction":   "a very long unreferenced instruction blob",
	})

	result, err := Status(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	sum := Summarize(result)

	if sum.Identity.SessionName != "owner/repo-1" {
		t.Errorf("Identity.SessionName = %q", sum.Identity.SessionName)
	}
	if len(sum.Work) != 1 {
		t.Fatalf("len(Work) = %d, want 1 (only the done_when-bearing instance)", len(sum.Work))
	}
	w := sum.Work[0]
	if w.Instance != "work" {
		t.Errorf("Instance = %q, want work", w.Instance)
	}
	if w.DoneWhen == nil || len(w.DoneWhen.Leaves) != 2 {
		t.Fatalf("DoneWhen = %+v, want 2 leaves", w.DoneWhen)
	}
	b, err := json.Marshal(sum)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "instruction") {
		t.Errorf("summary JSON leaked an output not referenced by done_when: %s", b)
	}
	if len(w.Chains) != 1 || w.Chains[0].ChainID != "review" {
		t.Fatalf("Chains = %+v, want one entry for chain \"review\"", w.Chains)
	}
}
