package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/eventlog"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/contracts/event"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// workTaskWithChain is a "work" task fixture: green-checks + pending-judge
// done_when, a revision/checks_status outputs contract, and the given
// `[[chains]]` (and any other extra TOML) appended. Every chain test now
// declares its chain this way — a task-embedded [[chains]] is the only
// source a chain can come from since the legacy chains/*.toml dual-read was
// retired (story PR-D #5).
func workTaskWithChain(extraChain string) taskFixture {
	return taskFixture{
		id:    "work",
		scope: "session",
		setup: "echo '{}'",
		extra: `
[outputs_schema]
type = "object"
[outputs_schema.properties.revision]
type = "string"
[outputs_schema.properties.checks_status]
type = "string"

[done_when]
all = [
  { check = "checks_status", in = ["SUCCESS"] },
  { judge = "ac met", id = "ac-met" },
]
` + extraChain,
	}
}

func seedReviewWork(t *testing.T, store *state.Store, name string, outputs map[string]any) {
	t.Helper()
	seedSession(t, store, name, "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"work": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Outputs: outputs,
		},
	})
}

func writeWorkflowFile(t *testing.T, cfg *config.Config, id, body string) {
	t.Helper()
	dir := filepath.Join(cfg.BaseDir, "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSpawn(spawns []ChainSpawn, chainID string) (ChainSpawn, bool) {
	for _, sp := range spawns {
		if sp.ChainID == chainID {
			return sp, true
		}
	}
	return ChainSpawn{}, false
}

func TestCheckSession_ChainFiresWhenWhenAndOutputsHold(t *testing.T) {
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
	// A parent so a sibling reviewer would be tree-attached.
	seedSession(t, store, "owner/repo-orch", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orch")

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, ok := findSpawn(res.Chains, "review")
	if !ok {
		t.Fatalf("review chain not evaluated: %+v", res.Chains)
	}
	if !sp.Fired || sp.BlockedReason != "" {
		t.Fatalf("expected fired, got %+v", sp)
	}
	if sp.Placement != config.ChainPlacementSibling || sp.ParentSession != "owner/repo-orch" {
		t.Fatalf("placement/parent = %q/%q", sp.Placement, sp.ParentSession)
	}
	if sp.TargetSession != "owner/repo-1+review-work" {
		t.Fatalf("target = %q", sp.TargetSession)
	}
	if len(sp.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", sp.Warnings)
	}
}

// A chain's `workflow` field can be a template over `.Work.workflow` (the work
// session's own workflow) — the cross-tool review-chain shape config/sennit
// ships: whichever tool wrote the code, the reviewer is the other one.
func TestCheckSession_ChainWorkflowTemplateCrossTool(t *testing.T) {
	const crossToolTmpl = `{{if eq .Work.workflow "claude"}}codex{{else}}claude{{end}}`
	tests := []struct {
		workSessionWorkflow  string
		wantReviewerWorkflow string
	}{
		{"claude", "codex"},
		{"codex", "claude"},
	}
	for _, tc := range tests {
		t.Run(tc.workSessionWorkflow, func(t *testing.T) {
			store := testStore(t)
			cfg := writeWorkflowFixture(t, t.TempDir(), tc.workSessionWorkflow,
				[]taskFixture{workTaskWithChain(fmt.Sprintf(`
[[chains]]
id       = "review"
workflow = %q
[chains.when]
all = [
  { judge_pending = "ac-met" },
  { check = "checks_status", in = ["SUCCESS"] },
]
`, crossToolTmpl))},
				[]nodeFixture{{id: "work"}})
			// writeWorkflowFixture already wrote workSessionWorkflow; the
			// reviewer's target workflow (the opposite tool) must also exist
			// for the fire's workflow-resolution check to pass.
			writeWorkflowFile(t, cfg, tc.wantReviewerWorkflow, "")
			seedSession(t, store, "owner/repo-1", "owner/repo", 1, tc.workSessionWorkflow, map[string]*contract.TaskState{
				"work": {
					Scope:   contract.TaskScopeSession,
					TaskID:  "work",
					Status:  contract.TaskStatusProduced,
					Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
				},
			})

			res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
			if err != nil {
				t.Fatalf("CheckSession: %v", err)
			}
			sp, ok := findSpawn(res.Chains, "review")
			if !ok || !sp.Fired || sp.BlockedReason != "" {
				t.Fatalf("expected fired, got %+v (found=%v)", sp, ok)
			}
			if sp.Workflow != tc.wantReviewerWorkflow {
				t.Fatalf("sp.Workflow = %q, want %q", sp.Workflow, tc.wantReviewerWorkflow)
			}
		})
	}
}

// An unrendered workflow template (e.g. a stray reference to a nonexistent
// context key) blocks the fire with an explicit reason rather than silently
// spawning nothing or crashing.
func TestCheckSession_ChainWorkflowTemplateRenderErrorBlocks(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "{{.Work.outputs.nonexistent_workflow_key}}"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.Fired || sp.BlockedReason != chainBlockedWorkflowUnresolved {
		t.Fatalf("expected workflow_unresolved block, got %+v", sp)
	}
}

// A fired chain renders its [chains.inputs] bindings against the work facts and
// attaches the resolved inputs to the spawn (passed to Up on a real spawn).
func TestCheckSession_ChainRendersAndAttachesInputs(t *testing.T) {
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
revision     = "{{.Work.outputs.revision}}"
work_session = "{{.Work.session}}"
resource     = "{{.Work.resource}}"
judge_ids    = "{{.Work.done_when.pending_judge_ids}}"
`)},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if !sp.Fired {
		t.Fatalf("expected fired, got %+v", sp)
	}
	if sp.Inputs["revision"] != "sha1" {
		t.Errorf("revision input = %v, want sha1", sp.Inputs["revision"])
	}
	if sp.Inputs["work_session"] != "owner/repo-1" {
		t.Errorf("work_session input = %v, want owner/repo-1", sp.Inputs["work_session"])
	}
	if sp.Inputs["judge_ids"] != "ac-met" {
		t.Errorf("judge_ids input = %v, want ac-met", sp.Inputs["judge_ids"])
	}
	if sp.Inputs["resource"] == "" {
		t.Errorf("resource input is empty; want the work resource")
	}
}

// Resolved inputs that violate the spawned workflow's inputs contract block the
// fire rather than spawning a reviewer with contract-violating inputs. Unlike
// an undeclared-upstream-output wiring (now rejected at config load time by
// validateTaskChains), the downstream workflow's inputs_schema is a separate
// contract sennit does not cross-check until fire time.
func TestCheckSession_ChainBlockedDownstreamInputsContract(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" }, { check = "checks_status", in = ["SUCCESS"] } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`)},
		[]nodeFixture{{id: "work"}})
	// codex declares a closed inputs contract, so the wired `revision` is not an
	// accepted input.
	writeWorkflowFile(t, cfg, "codex", `
[inputs_schema]
type = "object"
additionalProperties = false
`)
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.Fired || sp.BlockedReason != chainBlockedInvalidBindings {
		t.Fatalf("expected invalid_bindings block, got %+v", sp)
	}
	if !strings.Contains(strings.Join(sp.Warnings, " "), "inputs contract") {
		t.Fatalf("expected warning about inputs contract, got %+v", sp.Warnings)
	}
}

func TestCheckSession_ChainBlockedWhenUnmet(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" }, { check = "checks_status", in = ["SUCCESS"] } ]
`)},
		[]nodeFixture{{id: "work"}})
	// checks_status FAILURE → when unmet even though judge is pending.
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "FAILURE", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.Fired || sp.BlockedReason != chainBlockedWhenUnmet {
		t.Fatalf("expected when_unmet, got %+v", sp)
	}
}

func TestCheckSession_ChainBlockedOutputsMissing(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" }, { check = "checks_status", in = ["SUCCESS"] } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
`)},
		[]nodeFixture{{id: "work"}})
	// when holds, but the wired `revision` output is absent → not fired.
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.Fired {
		t.Fatalf("expected blocked, got fired: %+v", sp)
	}
	if sp.BlockedReason != chainBlockedOutputsMissing || len(sp.MissingOutputs) != 1 || sp.MissingOutputs[0] != "revision" {
		t.Fatalf("expected outputs_missing[revision], got %+v", sp)
	}
}

func TestCheckSession_ChainChildPlacementParent(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id        = "review"
workflow  = "codex"
placement = "child"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if !sp.Fired || sp.Placement != config.ChainPlacementChild || sp.ParentSession != "owner/repo-1" {
		t.Fatalf("expected child of work session, got %+v", sp)
	}
}

// A sibling chain on a parentless work session spawns under that session's
// own implicit root (domain.ImplicitRootParent) rather than being left
// unreachable — the reviewer is opted into the work session's sibling group,
// not its parent's (there is none).
func TestCheckSession_ChainSiblingWithoutParentUsesImplicitRoot(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	// No parent set on the work session.
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.ParentSession != "root:owner/repo-1" {
		t.Fatalf("ParentSession = %q, want %q", sp.ParentSession, "root:owner/repo-1")
	}
}

// sennit tick spawns a fired, not-already-active chain; a chain whose target
// session already exists is reported already-active rather than re-spawned.
func TestTickSession_ChainAlreadyActiveSkips(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
judge_ids = "{{.Work.done_when.pending_judge_ids}}"
pr_url = "{{.Work.resource}}"
`)},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})
	// The reviewer session already exists → fire is idempotent (no spawn).
	seedSession(t, store, "owner/repo-1+review-work", "owner/repo", 1, "codex", nil)

	res, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1", SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if !sp.Fired || !sp.AlreadyActive || sp.Spawned {
		t.Fatalf("expected already-active without spawn, got %+v", sp)
	}
	if !sp.KickDelivered || sp.KickDebounced {
		t.Fatalf("expected already-active kick delivery, got %+v", sp)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1+review-work", 0, event.Filter{
		Types: []string{event.TypeUserEmit},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("user.emit count = %d, want 1: %+v", len(evs), evs)
	}
	if evs[0].Source != event.SourceTick || evs[0].Metadata["revision"] != "sha1" || evs[0].Metadata["judge_ids"] != "ac-met" {
		t.Fatalf("unexpected kick event: %+v", evs[0])
	}

	res2, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1", SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession(2): %v", err)
	}
	sp2, _ := findSpawn(res2.Chains, "review")
	if !sp2.Fired || !sp2.AlreadyActive || sp2.Spawned || !sp2.KickDebounced {
		t.Fatalf("expected already-active debounced on same revision, got %+v", sp2)
	}
	evs, _, _, err = eventlog.NewStore(store.Dir()).List("owner/repo-1+review-work", 0, event.Filter{
		Types: []string{event.TypeUserEmit},
	})
	if err != nil {
		t.Fatalf("List(2): %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("same revision should not emit a duplicate kick, got %d events: %+v", len(evs), evs)
	}
}

// A chain's spawn (Up) failing must not discard the done_when actions this
// same tick already published/persisted, nor abort the whole tick result —
// only that chain's own entry reports the failure, so the next tick can
// retry the (idempotent) fire. The resolved workflow itself must exist here
// (an undefined workflow is caught earlier, at evalChain, as
// workflow_unresolved — see TestTickSession_ChainWorkflowUnresolvedBlocksFire
// below) — this exercises a spawn failure for an unrelated reason, so the
// resource_allowlist boundary is set to deny the resource.
func TestTickSession_ChainSpawnFailureIsNonFatal(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	cfg.ResourceAllowlist = []string{"^$"} // matches nothing: Create() rejects every resource
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1", SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(res.Actions) == 0 {
		t.Fatalf("expected done_when actions to still be reported despite the chain spawn failure, got %+v", res.Actions)
	}
	sp, ok := findSpawn(res.Chains, "review")
	if !ok || !sp.Fired || sp.Spawned {
		t.Fatalf("expected fired-but-not-spawned, got %+v", sp)
	}
	if len(sp.Warnings) == 0 || !strings.Contains(strings.Join(sp.Warnings, " "), "spawn failed") {
		t.Fatalf("expected a spawn-failed warning, got %+v", sp.Warnings)
	}
}

// AC1: a chain resolving (statically or via its `workflow` template) to a
// workflow ID with no `.sennit/workflows/<id>.toml` definition must not report
// fired — that would let sennit tick attempt (and identically fail) the same
// spawn every tick, which reads as a silent repeating failure rather than the
// explicit error AC1 requires. It blocks instead, with a reason a caller can
// distinguish from every other blocked reason.
func TestTickSession_ChainWorkflowUnresolvedBlocksFire(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "missing-workflow"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})

	res, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1", SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	sp, ok := findSpawn(res.Chains, "review")
	if !ok {
		t.Fatalf("review chain not evaluated: %+v", res.Chains)
	}
	if sp.Fired || sp.Spawned || sp.BlockedReason != chainBlockedWorkflowUnresolved {
		t.Fatalf("expected workflow_unresolved block without a spawn attempt, got %+v", sp)
	}
	if !strings.Contains(strings.Join(sp.Warnings, " "), "missing-workflow") {
		t.Fatalf("expected a warning naming the unresolved workflow, got %+v", sp.Warnings)
	}
}

func TestCheckSession_NoChainsIsEmpty(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{BaseDir: t.TempDir(), WorktreesRoot: t.TempDir()}
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})
	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	if len(res.Chains) != 0 {
		t.Fatalf("expected no chains, got %+v", res.Chains)
	}
}

// A satisfied judge leaf (approve at the current revision) leaves judge_pending
// false, so the review chain does not fire — the gate is closed only by a real
// independent verdict.
func TestCheckSession_ChainSatisfiedJudgeDoesNotFire(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	seedReviewWork(t, store, "owner/repo-1", map[string]any{"checks_status": "SUCCESS", "revision": "sha1"})
	seedSession(t, store, "owner/repo-orch", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1+review-work", "owner/repo", 1, "codex", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orch")
	setParent(t, store, "owner/repo-1+review-work", "owner/repo-orch")
	// Record an approving sibling verdict at the current revision.
	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "work",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "verified",
		Revision:        "sha1",
		ReviewerSession: "owner/repo-1+review-work",
	}); err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if sp.Fired {
		t.Fatalf("expected not fired (judge satisfied), got %+v", sp)
	}
}

// AC1: a task-declared chain is evaluated only against instances of the task
// that declared it. A different task instance sharing the same judge id is
// not a candidate at all — not merely blocked, absent from the plan.
func TestCheckSession_ChainTaskScopedEvaluation(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{
			{
				id:    "a",
				scope: "session",
				setup: "echo '{}'",
				extra: `
[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]

[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`,
			},
			{
				id:    "b",
				scope: "session",
				setup: "echo '{}'",
				extra: `
[done_when]
all = [ { judge = "ac met", id = "ac-met" } ]
`,
			},
		},
		[]nodeFixture{{id: "a"}, {id: "b"}})
	writeWorkflowFile(t, cfg, "codex", "")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"a": {Scope: contract.TaskScopeSession, TaskID: "a", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		"b": {Scope: contract.TaskScopeSession, TaskID: "b", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	var aEntries, bEntries int
	for _, sp := range res.Chains {
		switch sp.Instance {
		case "a":
			aEntries++
			if sp.Task != "a" || !sp.Fired {
				t.Errorf("instance a spawn = %+v, want fired for task a", sp)
			}
		case "b":
			bEntries++
		}
	}
	if aEntries != 1 {
		t.Fatalf("expected exactly 1 evaluation for instance a, got %d", aEntries)
	}
	if bEntries != 0 {
		t.Fatalf("expected task b instance to have no chain candidates at all, got %d entries", bEntries)
	}
}

// AC5 (dual-read retired): a legacy chains/*.toml file sitting under the
// config base dir contributes nothing to the evaluated plan — it is not
// merged, never fires. It is still surfaced as a migration-nudge warning
// (config.LegacyChainsDirNotice) so a straggler file isn't silently inert
// with zero signal.
func TestCheckSession_LegacyChainsDirIsIgnored(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{
			{id: "a", scope: "session", setup: "echo '{}'", extra: "[done_when]\nall = [ { judge = \"ac met\", id = \"ac-met\" } ]\n"},
		},
		[]nodeFixture{{id: "a"}})
	legacyDir := filepath.Join(cfg.BaseDir, "chains")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "review.toml"), []byte(`
[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"a": {Scope: contract.TaskScopeSession, TaskID: "a", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})

	res, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	if len(res.Chains) != 0 {
		t.Fatalf("expected the legacy chains/*.toml declaration to be ignored, got %+v", res.Chains)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "review.toml") {
		t.Fatalf("expected one warning naming the ignored file, got %+v", res.Warnings)
	}
}

// Issue #6: a work session tracked by an issue in one repository can open its
// pull request in a different repository. Resource observation stays
// VCS-agnostic at the core level (it never searches a second repository on
// the work session's behalf) — instead, an explicit `pr_url` recorded via
// `sennit state set-output` takes precedence over resource-derived
// resolution, and a refresh that finds nothing for `pr_url` leaves that
// explicit value untouched. This lets the chain's wired `pr_url` output
// resolve and the reviewer chain fire without orchestrator involvement.
func TestTickSession_ChainFiresOnExplicitPRURLAcrossRepos(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{
			id:    "work",
			scope: "session",
			setup: "echo '{}'",
			extra: `
[outputs_schema]
type = "object"
[outputs_schema.properties.revision]
type = "string"
[outputs_schema.properties.checks_status]
type = "string"
[outputs_schema.properties.pr_url]
type    = "string"
mutable = true

[done_when]
all = [
  { check = "checks_status", in = ["SUCCESS"] },
  { judge = "ac met", id = "ac-met" },
]

[[outputs]]
produces           = ["pr_url"]
from_resource_status = true

[[chains]]
id       = "review"
workflow = "codex"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
[chains.inputs]
revision = "{{.Work.outputs.revision}}"
pr_url   = "{{.Work.outputs.pr_url}}"
`,
		}},
		[]nodeFixture{{id: "work"}})
	writeWorkflowFile(t, cfg, "codex", "")
	// The resource definition observes the tracked issue but, by design,
	// never searches a second repository for its pull request — it reports
	// only what same-repo observation can see.
	writeResourceDefFixture(t, cfg.BaseDir, "github", `
match   = '^https://github\.com/'
observe = "echo '{\"checks_status\":\"SUCCESS\"}'"
`)

	const crossRepoPRURL = "https://github.com/owner/repo-2/pull/42"
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "wf", map[string]*contract.TaskState{
		"work": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			// Tracked by an issue in a different repository than the PR.
			Resource: "https://github.com/owner/repo/issues/1",
			Outputs:  map[string]any{"revision": "sha1", "checks_status": "SUCCESS"},
		},
	})

	// The work session records the PR itself, in the other repository.
	if _, err := SetOutput(cfg, store, SetOutputParams{
		Identifier: "owner/repo-1",
		Node:       "work",
		Outputs:    map[string]any{"pr_url": crossRepoPRURL},
	}); err != nil {
		t.Fatalf("SetOutput: %v", err)
	}

	res, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}

	sp, _ := findSpawn(res.Chains, "review")
	if !sp.Fired {
		t.Fatalf("expected the review chain to fire, got %+v", sp)
	}
	if sp.Inputs["pr_url"] != crossRepoPRURL {
		t.Fatalf("pr_url input = %v, want %q", sp.Inputs["pr_url"], crossRepoPRURL)
	}

	s := store.Get("owner/repo-1")
	if got := s.Tasks["work"].Outputs["pr_url"]; got != crossRepoPRURL {
		t.Fatalf("persisted pr_url = %v, want %q (refresh must not clobber an explicit value it found nothing to replace)", got, crossRepoPRURL)
	}
}
