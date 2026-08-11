package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/eventlog"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/contracts/event"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

func TestRecordJudge_PersistsReviewerInput(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"revision": "sha1"},
		},
	})

	result, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "AC verified in test",
		ReviewerSession: "owner/repo-1+review",
	})
	if err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}
	if result.Revision != "sha1" {
		t.Fatalf("revision = %q, want sha1", result.Revision)
	}

	st := store.Get("owner/repo-1").Tasks["initial"]
	got := st.DoneWhen.Judges["ac-met"]
	if got == nil {
		t.Fatal("missing persisted judge")
	}
	if got.Action != "approve" || got.Reason != "AC verified in test" || got.ReviewerSession != "owner/repo-1+review" {
		t.Fatalf("judge = %+v", got)
	}
	if got.TargetSession != "owner/repo-1" || got.Instance != "initial" || got.Revision != "sha1" {
		t.Fatalf("judge target/instance/revision = %+v", got)
	}
}

// TestRecordJudge_AppendsJudgeRecordedEvent covers the tick reactor's judge
// builtin trigger (AC2, ADR amendment 2026-07-04 §1): recording a judge must
// append sennit.judge.recorded to the *target* work session's own log, not the
// reviewer's, since the reactor ticks the judged session, not the recorder.
func TestRecordJudge_AppendsJudgeRecordedEvent(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	work := "owner/repo-1"
	reviewer := "owner/repo-1+review"
	seedSession(t, store, work, "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"revision": "sha1"},
		},
	})

	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     work,
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "AC verified in test",
		ReviewerSession: reviewer,
	}); err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}

	log := eventlog.NewStore(store.Dir())
	onTarget, _, _, err := log.List(work, 0, event.Filter{Types: []string{event.TypeJudgeRecorded}})
	if err != nil {
		t.Fatalf("list target events: %v", err)
	}
	if len(onTarget) != 1 {
		t.Fatalf("target session events = %+v, want exactly one sennit.judge.recorded", onTarget)
	}
	onReviewer, _, _, err := log.List(reviewer, 0, event.Filter{Types: []string{event.TypeJudgeRecorded}})
	if err != nil {
		t.Fatalf("list reviewer events: %v", err)
	}
	if len(onReviewer) != 0 {
		t.Fatalf("reviewer session events = %+v, want none (the trigger ticks the target, not the recorder)", onReviewer)
	}
}

// The verdict stamps the reviewer→target tree relation and the reviewer's
// workflow at record time, so the projection reads them off the record without
// re-walking the tree or assuming the reviewer session still exists.
func TestRecordJudge_StampsRelationAndWorkflow(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	work := "owner/repo-1"
	reviewer := "owner/repo-1+review"
	seedSession(t, store, work, "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"revision": "sha1"},
		},
	})
	// Make the reviewer a sibling of the work session under a shared parent.
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, reviewer, "owner/repo", 1, "codex", nil)
	setParent(t, store, work, "owner/repo-orchestrator")
	setParent(t, store, reviewer, "owner/repo-orchestrator")

	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     work,
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "AC verified",
		ReviewerSession: reviewer,
	}); err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}

	got := store.Get(work).Tasks["initial"].DoneWhen.Judges["ac-met"]
	if got.Relation != string(domain.RelationSibling) {
		t.Fatalf("relation = %q, want sibling", got.Relation)
	}
	if got.ReviewerWorkflow != "codex" {
		t.Fatalf("reviewer_workflow = %q, want codex", got.ReviewerWorkflow)
	}
}

// Records written before relation/workflow were stamped carry neither; the read
// path derives both from the live tree so old verdicts still project.
func TestJudgeInputs_DerivesRelationForLegacyRecords(t *testing.T) {
	work := "owner/repo-1"
	reviewer := "owner/repo-1+review"
	parent := "owner/repo-orchestrator"
	sessions := map[string]*domain.Session{
		parent:   {Name: parent},
		work:     {Name: work, ParentSession: parent},
		reviewer: {Name: reviewer, ParentSession: parent, Workflow: "codex"},
	}
	legacy := map[string]*contract.DoneWhenJudge{
		"ac-met": {LeafID: "ac-met", Action: "approve", Revision: "sha1", ReviewerSession: reviewer},
	}

	got := judgeInputs(legacy, work, sessions)["ac-met"]
	if got.Relation != string(domain.RelationSibling) {
		t.Fatalf("derived relation = %q, want sibling", got.Relation)
	}
	if got.ReviewerWorkflow != "codex" {
		t.Fatalf("derived reviewer_workflow = %q, want codex", got.ReviewerWorkflow)
	}
}

// A new record (relation stamped) is self-contained: an empty reviewer workflow
// is a record-time fact, not a gap to backfill, so the projection must NOT
// re-derive it from a live reviewer session that later gained a workflow.
func TestJudgeInputs_NewRecordHonorsStampedEmptyWorkflow(t *testing.T) {
	work := "owner/repo-1"
	reviewer := "owner/repo-1+review"
	parent := "owner/repo-orchestrator"
	sessions := map[string]*domain.Session{
		parent:   {Name: parent},
		work:     {Name: work, ParentSession: parent},
		reviewer: {Name: reviewer, ParentSession: parent, Workflow: "codex"},
	}
	rec := map[string]*contract.DoneWhenJudge{
		"ac-met": {
			LeafID:           "ac-met",
			Action:           "approve",
			Revision:         "sha1",
			ReviewerSession:  reviewer,
			Relation:         string(domain.RelationSibling),
			ReviewerWorkflow: "", // stamped empty at record time
		},
	}

	got := judgeInputs(rec, work, sessions)["ac-met"]
	if got.ReviewerWorkflow != "" {
		t.Fatalf("reviewer_workflow = %q, want empty (record-time fact, not re-derived)", got.ReviewerWorkflow)
	}
	if got.Relation != string(domain.RelationSibling) {
		t.Fatalf("relation = %q, want sibling (stamped)", got.Relation)
	}
}

func TestRecordJudge_RequiresRevision(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Dynamic: true},
	})

	_, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "AC verified",
		ReviewerSession: "review",
	})
	if err == nil {
		t.Fatal("expected missing revision error")
	}
}

func TestRecordJudge_RequiresReviewerSession(t *testing.T) {
	t.Setenv("SENNIT_SESSION_NAME", "")
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"revision": "sha1"},
		},
	})

	_, err := RecordJudge(cfg, store, JudgeParams{
		SessionName: "owner/repo-1",
		Instance:    "initial",
		LeafID:      "ac-met",
		Action:      "approve",
		Reason:      "AC verified",
	})
	if err == nil {
		t.Fatal("expected missing reviewer error")
	}
}

// A session cannot record a verdict on its own judge leaf: self-review is barred
// at record time, independent of any leaf relation policy.
func TestRecordJudge_RejectsSelfReview(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	work := "owner/repo-1"
	seedSession(t, store, work, "owner/repo", 1, "", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"revision": "sha1"},
		},
	})

	_, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     work,
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "I reviewed my own work",
		ReviewerSession: work,
	})
	if err == nil || !strings.Contains(err.Error(), "self-review") {
		t.Fatalf("self-review RecordJudge err = %v, want self-review rejection", err)
	}
}

// Default policy is sibling/parent: a sibling reviewer's approval satisfies the
// leaf, but a child reviewer's identical approval is pending (relation not
// accepted) until the leaf opts children in.
func TestCheckSession_DefaultPolicyAcceptsSiblingRejectsChild(t *testing.T) {
	judgeLeaf := `{"all":[{"judge":"AC met","id":"ac-met"}]}`

	t.Run("sibling approval satisfies", func(t *testing.T) {
		store := testStore(t)
		cfg := checkStatusOnlyConfig(t, 1)
		work, reviewer := "owner/repo-1", "owner/repo-1+review"
		seedJudgeWork(t, store, work, judgeLeaf)
		seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
		seedSession(t, store, reviewer, "owner/repo", 1, "codex", nil)
		setParent(t, store, work, "owner/repo-orchestrator")
		setParent(t, store, reviewer, "owner/repo-orchestrator")
		recordApproval(t, cfg, store, work, reviewer)

		result := checkActions(t, cfg, store, work)
		if len(result) != 1 || result[0].Action != "satisfied" {
			t.Fatalf("actions = %+v, want satisfied for sibling reviewer", result)
		}
	})

	t.Run("child approval is pending relation_not_accepted", func(t *testing.T) {
		store := testStore(t)
		cfg := checkStatusOnlyConfig(t, 1)
		work, reviewer := "owner/repo-1", "owner/repo-1+child"
		seedJudgeWork(t, store, work, judgeLeaf)
		seedSession(t, store, reviewer, "owner/repo", 1, "codex", nil)
		setParent(t, store, reviewer, work)
		recordApproval(t, cfg, store, work, reviewer)

		result := checkActions(t, cfg, store, work)
		if len(result) != 1 || result[0].Action != "review_required" || result[0].UnmetItems[0].PendingReason != "relation_not_accepted" {
			t.Fatalf("actions = %+v, want relation_not_accepted pending for child reviewer", result)
		}
	})
}

// A leaf that opts children in (relation = ["child"]) accepts a child reviewer's
// approval that the default policy would reject.
func TestCheckSession_ExplicitChildRelationAccepted(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	work, reviewer := "owner/repo-1", "owner/repo-1+child"
	seedJudgeWork(t, store, work, `{"all":[{"judge":"AC met","id":"ac-met","relation":["child"]}]}`)
	seedSession(t, store, reviewer, "owner/repo", 1, "codex", nil)
	setParent(t, store, reviewer, work)
	recordApproval(t, cfg, store, work, reviewer)

	result := checkActions(t, cfg, store, work)
	if len(result) != 1 || result[0].Action != "satisfied" {
		t.Fatalf("actions = %+v, want satisfied for explicitly allowed child reviewer", result)
	}
}

func seedJudgeWork(t *testing.T, store *state.Store, work, judgeLeaf string) {
	t.Helper()
	seedSession(t, store, work, "owner/repo", 1, "claude", map[string]*contract.TaskState{
		"initial": {
			Scope:         contract.TaskScopeSession,
			TaskID:        "work",
			Status:        contract.TaskStatusProduced,
			Dynamic:       true,
			Outputs:       map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
			ExtraDoneWhen: []byte(judgeLeaf),
		},
	})
}

func recordApproval(t *testing.T, cfg *config.Config, store *state.Store, work, reviewer string) {
	t.Helper()
	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     work,
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "AC verified",
		ReviewerSession: reviewer,
	}); err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}
}

func checkActions(t *testing.T, cfg *config.Config, store *state.Store, work string) []CheckAction {
	t.Helper()
	result, err := CheckSession(cfg, store, CheckParams{SessionName: work})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	return result.Actions
}

func TestTickSession_KicksUnsatisfiedAndPersistsRound(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
		},
	})

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "kick" || result.Actions[0].Round != 1 {
		t.Fatalf("actions = %+v", result.Actions)
	}
	if len(result.Actions[0].UnmetItems) != 1 || result.Actions[0].UnmetItems[0].Output != "checks_status" || result.Actions[0].UnmetItems[0].Value != "FAILURE" {
		t.Fatalf("unmet_items = %+v, want checks_status failure", result.Actions[0].UnmetItems)
	}
	check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen
	if check == nil || check.Rounds != 1 || check.LastAction != "kick" {
		t.Fatalf("done_when state = %+v", check)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{event.TypeUserEmit}})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 1 || !strings.Contains(evs[0].Body, "checks_status eq") {
		t.Fatalf("events = %+v", evs)
	}
}

// A dirty PR never gets a CI run from GitHub, so checks_status sits PENDING
// forever and the kick loop would otherwise repeat with no way out.
// mergeable_state is not a done_when leaf (a conflict is a work signal, not a
// failure), so the rebase hint must ride along in the kick body instead.
func TestTickSession_KickBodyAdvisesRebaseWhenMergeableStateDirty(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "PENDING", "revision": "sha1", "mergeable_state": "dirty"},
		},
	})

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "kick" {
		t.Fatalf("actions = %+v", result.Actions)
	}
	if !strings.Contains(result.Actions[0].Body, "mergeable_state=dirty") {
		t.Fatalf("kick body = %q, want a mergeable_state=dirty advisory", result.Actions[0].Body)
	}
}

// A clean (or unknown/NULL) mergeable_state must not perturb the existing
// kick body — the advisory is additive, not a replacement for the normal
// unmet-items text.
func TestTickSession_KickBodyOmitsRebaseHintWhenMergeableStateClean(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1", "mergeable_state": "clean"},
		},
	})

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "kick" {
		t.Fatalf("actions = %+v", result.Actions)
	}
	if strings.Contains(result.Actions[0].Body, "mergeable_state") {
		t.Fatalf("kick body = %q, want no mergeable_state mention", result.Actions[0].Body)
	}
}

// The kick event's Body has always carried the unmet items as prose; the
// structured CheckUnmetItem list itself (kind/output/value/pending_reason)
// must also reach the delivered event, not just tick's own JSON response, so
// a receiving agent doesn't have to parse the bullet list.
func TestTickSession_KickCarriesStructuredUnmetItemsInEventMetadata(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
		},
	})

	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("TickSession: %v", err)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{event.TypeUserEmit}})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	raw := evs[0].Metadata["unmet_items"]
	if raw == "" {
		t.Fatal("expected the kick event to carry unmet_items metadata")
	}
	var items []CheckUnmetItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("unmet_items metadata is not valid JSON: %v (%s)", err, raw)
	}
	if len(items) != 1 || items[0].Output != "checks_status" || items[0].Value != "FAILURE" {
		t.Fatalf("unmet_items metadata = %+v, want checks_status failure", items)
	}
}

// TestTickSession_StampsLastTickAt covers the reactor's `heartbeat` sweep
// precondition: every tick resets the session-level watermark, even for an
// instance with no done_when leaves to evaluate.
func TestTickSession_StampsLastTickAt(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)

	before := store.Get("owner/repo-1").LastTickAt
	if !before.IsZero() {
		t.Fatalf("LastTickAt = %v before any tick, want zero", before)
	}
	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	after := store.Get("owner/repo-1").LastTickAt
	if after.IsZero() {
		t.Fatal("LastTickAt still zero after TickSession")
	}
}

func TestTickSession_RepeatedPollDoesNotAdvanceRound(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 1)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})

	first, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	second, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("second TickSession: %v", err)
	}
	if first.Actions[0].Round != 1 || second.Actions[0].Round != 1 {
		t.Fatalf("rounds = first %d second %d, want both 1", first.Actions[0].Round, second.Actions[0].Round)
	}
	check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen
	if check == nil || check.Rounds != 1 {
		t.Fatalf("done_when state = %+v, want one persisted round", check)
	}
	if second.Actions[0].Action == "escalate" {
		t.Fatalf("repeated poll escalated: %+v", second.Actions[0])
	}
}

func TestTickSession_NewDoneWhenStateCanEscalateAfterMaxRounds(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 1)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Tasks["initial"].Outputs["revision"] = "sha2"
		return nil
	}); err != nil {
		t.Fatalf("update revision: %v", err)
	}

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("second TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "escalate" {
		t.Fatalf("actions = %+v, want escalation on new done_when state after max rounds", result.Actions)
	}
}

func TestCheckSession_MaxRoundsZeroIsUnbounded(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 0)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
			DoneWhen: &contract.DoneWhenState{
				Rounds: 100,
			},
		},
	})

	result, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "kick" || result.Actions[0].MaxRounds != 0 {
		t.Fatalf("actions = %+v, want unbounded kick with max_rounds=0", result.Actions)
	}
	if !strings.Contains(result.Actions[0].Body, "101/unbounded") {
		t.Fatalf("body = %q, want unbounded round text", result.Actions[0].Body)
	}
}

func TestTickSession_EscalatesAfterMaxRounds(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
			DoneWhen: &contract.DoneWhenState{
				Rounds: 1,
			},
		},
	})

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "escalate" {
		t.Fatalf("actions = %+v", result.Actions)
	}
	if !strings.Contains(result.Actions[0].Body, "1/1 round") || !strings.Contains(result.Actions[0].Body, "observed FAILURE") {
		t.Fatalf("escalation body = %q, want round and unmet check context", result.Actions[0].Body)
	}
	check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen
	if check == nil || check.EscalateReason == "" || check.EscalatedAt.IsZero() {
		t.Fatalf("done_when state = %+v", check)
	}
}

func TestTickSession_EscalatesWhenJudgeKeepsRequestingChanges(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 1)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "https://github.com/owner/repo/pull/1",
			Outputs:  map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "request_changes",
		Reason:          "acceptance criterion still missing",
		ReviewerSession: "owner/repo-1+review",
	}); err != nil {
		t.Fatalf("RecordJudge: %v", err)
	}

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("second TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "escalate" {
		t.Fatalf("actions = %+v, want escalation after judge request_changes", result.Actions)
	}
	state := store.Get("owner/repo-1").Tasks["initial"].DoneWhen
	if state == nil || state.EscalateReason == "" || state.Judges["ac-met"].Action != "request_changes" {
		t.Fatalf("done_when state = %+v", state)
	}
}

func TestCheckSession_StableOrderSkipsFailedAndReportsMissingReviewerCommand(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 3)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"zeta": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
		"alpha": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
		"failed": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusFailed,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.ResourceID = ""
		return nil
	}); err != nil {
		t.Fatalf("clear resource: %v", err)
	}

	result, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("actions = %+v, want two produced instances", result.Actions)
	}
	if result.Actions[0].Instance != "alpha" || result.Actions[1].Instance != "zeta" {
		t.Fatalf("actions not sorted by instance: %+v", result.Actions)
	}
	for _, action := range result.Actions {
		if action.ReviewerCommand != "" || !strings.Contains(action.Body, "reviewer dispatch command unavailable") {
			t.Fatalf("action should report missing reviewer command: %+v", action)
		}
		if len(action.Warnings) != 1 || action.Warnings[0] != "reviewer dispatch command unavailable: task instance has no resource" {
			t.Fatalf("warnings = %+v, want reviewer dispatch unavailable warning", action.Warnings)
		}
	}
}

func TestTickScenario_RequestChangesStaleThenApproved(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 4)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "https://github.com/owner/repo/pull/1",
			Outputs:  map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	// Both reviewers are siblings of the work session under a shared parent, so
	// the default relation policy (sibling/parent) accepts their verdicts.
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1+review", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1+review2", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setParent(t, store, "owner/repo-1+review", "owner/repo-orchestrator")
	setParent(t, store, "owner/repo-1+review2", "owner/repo-orchestrator")

	first, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	if len(first.Actions) != 1 || first.Actions[0].Action != "review_required" || first.Actions[0].ReviewerCommand == "" {
		t.Fatalf("first actions = %+v", first.Actions)
	}
	if len(first.Actions[0].UnmetItems) != 1 || first.Actions[0].UnmetItems[0].ID != "ac-met" || first.Actions[0].UnmetItems[0].PendingReason != "missing_judge" {
		t.Fatalf("first unmet_items = %+v", first.Actions[0].UnmetItems)
	}
	if len(first.Actions[0].JudgeCommands) != 2 || !strings.Contains(first.Actions[0].Body, "sennit judge approve") || !strings.Contains(first.Actions[0].Body, "sennit judge request-changes") {
		t.Fatalf("first judge command contract = commands %+v body %q", first.Actions[0].JudgeCommands, first.Actions[0].Body)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{"sennit.tick.review_required"}})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 1 || !strings.Contains(evs[0].Body, "sennit judge approve") {
		t.Fatalf("review_required events = %+v, want reviewer instruction body", evs)
	}

	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "request_changes",
		Reason:          "missing acceptance criterion",
		ReviewerSession: "owner/repo-1+review",
	}); err != nil {
		t.Fatalf("record request_changes: %v", err)
	}
	second, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("second TickSession: %v", err)
	}
	if len(second.Actions) != 1 || second.Actions[0].Action != "kick" || !strings.Contains(second.Actions[0].Body, "missing acceptance criterion") {
		t.Fatalf("second actions = %+v", second.Actions)
	}

	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Tasks["initial"].Outputs["revision"] = "sha2"
		return nil
	}); err != nil {
		t.Fatalf("update revision: %v", err)
	}
	third, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("third TickSession: %v", err)
	}
	if len(third.Actions) != 1 || third.Actions[0].Action != "review_required" || !strings.Contains(third.Actions[0].Body, "stale_judge") {
		t.Fatalf("third actions = %+v", third.Actions)
	}
	if got := third.Actions[0].UnmetItems[0]; got.Revision != "sha1" || got.CurrentRevision != "sha2" || got.PendingReason != "stale_judge" {
		t.Fatalf("third unmet item = %+v, want stale judge revision context", got)
	}

	if _, err := RecordJudge(cfg, store, JudgeParams{
		SessionName:     "owner/repo-1",
		Instance:        "initial",
		LeafID:          "ac-met",
		Action:          "approve",
		Reason:          "acceptance criterion now covered",
		ReviewerSession: "owner/repo-1+review2",
	}); err != nil {
		t.Fatalf("record approve: %v", err)
	}
	fourth, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("fourth TickSession: %v", err)
	}
	if len(fourth.Actions) != 1 || fourth.Actions[0].Action != "satisfied" {
		t.Fatalf("fourth actions = %+v", fourth.Actions)
	}
}

func checkFixtureConfig(t *testing.T) *config.Config {
	t.Helper()
	return checkScenarioConfig(t, 1)
}

func checkScenarioConfig(t *testing.T, maxRounds int) *config.Config {
	t.Helper()
	return writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:    "work",
		scope: contract.TaskScopeSession,
		extra: fmt.Sprintf(`
requires = ["checks_status"]

[done_when]
all = [
  { check = "checks_status", eq = "SUCCESS" },
  { judge = "AC met", id = "ac-met" },
]

[done_when.budget]
max_rounds = %d

[outputs_schema]
type = "object"

[outputs_schema.properties]
checks_status = { type = "string", mutable = true }
revision = { type = "string", mutable = true }
`, maxRounds),
	}}, []nodeFixture{{id: "initial", uses: "work"}})
}

// The satisfied action pushes a `done` terminal event one hop to the parent
// (ADR: cross-session terminal event propagation, D1/D8), exactly once even
// across repeated polls of an already-satisfied instance.
func TestTickSession_PushesDoneToParentOnceOnSatisfied(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	first, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	if len(first.Actions) != 1 || first.Actions[0].Action != "satisfied" || first.Actions[0].Summary == "" {
		t.Fatalf("actions = %+v, want satisfied with a summary", first.Actions)
	}
	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("second TickSession: %v", err)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDone}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("done events on parent = %d, want 1 (pushed once, not re-pushed on repeated poll)", len(evs))
	}
	ev := evs[0]
	if ev.DeliveryMode != event.DeliveryModePush {
		t.Fatalf("delivery_mode = %q, want push", ev.DeliveryMode)
	}
	if ev.Metadata[event.MetaOriginSession] != "owner/repo-1" || ev.Metadata[event.MetaInstance] != "initial" {
		t.Fatalf("metadata = %+v", ev.Metadata)
	}
}

// A session with no parent (a tree root) has nothing to push `done` to; tick
// must still succeed.
func TestTickSession_SatisfiedWithNoParentDoesNotError(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "satisfied" {
		t.Fatalf("actions = %+v", result.Actions)
	}
}

// The escalate action pushes an `escalate` terminal event one hop to the
// parent, on top of the existing same-session sennit.tick.escalated record
// (kept for compat/observability, ADR D11 slice 5).
func TestTickSession_EscalatesAfterMaxRounds_PushesToParent(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
			DoneWhen: &contract.DoneWhenState{
				Rounds: 1,
			},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "escalate" {
		t.Fatalf("actions = %+v", result.Actions)
	}

	sameSession, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{"sennit.tick.escalated"}})
	if err != nil {
		t.Fatalf("list same-session: %v", err)
	}
	if len(sameSession) != 1 {
		t.Fatalf("same-session sennit.tick.escalated events = %d, want 1 (compat record kept)", len(sameSession))
	}

	pushed, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
	if err != nil {
		t.Fatalf("list parent: %v", err)
	}
	if len(pushed) != 1 {
		t.Fatalf("escalate events on parent = %d, want 1", len(pushed))
	}
	if pushed[0].Metadata[event.MetaOriginSession] != "owner/repo-1" || pushed[0].DeliveryMode != event.DeliveryModePush {
		t.Fatalf("pushed escalate = %+v", pushed[0])
	}
}

// TestTickSession_AutoRevivalAfterExhaustion covers auto-revival: once rounds are
// exhausted and a judge verdict has gone stale (a new revision landed), tick
// must revive the round budget on its own and deliver the standard
// re-evaluation kick to the recorded reviewer session, with no orchestrator
// involvement — and must never deliver a second kick for the same revision.
func TestTickSession_AutoRevivalAfterExhaustion(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 1)
	reviewer := "owner/repo-1+review"
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "https://github.com/owner/repo/pull/1",
			Outputs:  map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
			DoneWhen: &contract.DoneWhenState{
				Rounds:     1,
				LastAction: "escalate",
				Judges: map[string]*contract.DoneWhenJudge{
					"ac-met": {
						LeafID:          "ac-met",
						Action:          "request_changes",
						Reason:          "needs more work",
						Revision:        "sha1",
						ReviewerSession: reviewer,
						Relation:        string(domain.RelationSibling),
					},
				},
			},
		},
	})
	seedSession(t, store, reviewer, "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setParent(t, store, reviewer, "owner/repo-orchestrator")

	// A new revision lands: the recorded verdict is now stale.
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Tasks["initial"].Outputs["revision"] = "sha2"
		return nil
	}); err != nil {
		t.Fatalf("update revision: %v", err)
	}

	first, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("first TickSession: %v", err)
	}
	if len(first.Actions) != 1 || first.Actions[0].Action != "review_required" {
		t.Fatalf("first actions = %+v, want revived review_required instead of a repeat escalate", first.Actions)
	}
	if first.Actions[0].RevivalRevision != "sha2" {
		t.Fatalf("first action revival_revision = %q, want sha2", first.Actions[0].RevivalRevision)
	}

	log := eventlog.NewStore(store.Dir())
	kicks, _, _, err := log.List(reviewer, 0, event.Filter{Types: []string{event.TypeUserEmit}, Sources: []string{event.SourceTick}})
	if err != nil {
		t.Fatalf("list reviewer kicks: %v", err)
	}
	if len(kicks) != 1 {
		t.Fatalf("reviewer kicks = %d, want exactly 1 for revision sha2", len(kicks))
	}
	if !strings.Contains(kicks[0].Body, "sha2") {
		t.Fatalf("kick body = %q, want it to mention the new revision", kicks[0].Body)
	}

	st := store.Get("owner/repo-1").Tasks["initial"]
	if st.DoneWhen.Rounds >= 1 && st.DoneWhen.LastAction == "escalate" {
		t.Fatalf("done_when state did not revive: %+v", st.DoneWhen)
	}
	if st.DoneWhen.LastAutoRevivalRevision != "sha2" {
		t.Fatalf("last_auto_revival_revision = %q, want sha2 (dedup marker)", st.DoneWhen.LastAutoRevivalRevision)
	}

	// Ticking again at the same revision (repeated observation) must not
	// deliver a second kick.
	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err != nil {
		t.Fatalf("second TickSession: %v", err)
	}
	kicksAfter, _, _, err := log.List(reviewer, 0, event.Filter{Types: []string{event.TypeUserEmit}, Sources: []string{event.SourceTick}})
	if err != nil {
		t.Fatalf("list reviewer kicks after second tick: %v", err)
	}
	if len(kicksAfter) != 1 {
		t.Fatalf("reviewer kicks after repeated observation = %d, want still 1 (deduped)", len(kicksAfter))
	}
}

// blockSessionEventsDir makes eventlog.Append against the given session fail
// deterministically, by pre-creating a plain file where that session's own
// events subdirectory needs to be (so os.MkdirAll errors) — unlike
// blockEventsDir, it targets one session's log, leaving every other
// session's log writable.
func blockSessionEventsDir(t *testing.T, store *state.Store, session string) {
	t.Helper()
	dir := filepath.Join(store.Dir(), "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("blockSessionEventsDir: mkdir events root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, url.PathEscape(session)), []byte("block"), 0o644); err != nil {
		t.Fatalf("blockSessionEventsDir: %v", err)
	}
}

// TestTickSession_AutoRevivalReviewerPublishFailureRetries covers a
// reviewer-kick delivery failure: it must not let the revision dedup marker
// (LastAutoRevivalRevision) get stamped, or the
// reviewer would never be retried for a revision it never actually received.
func TestTickSession_AutoRevivalReviewerPublishFailureRetries(t *testing.T) {
	store := testStore(t)
	cfg := checkScenarioConfig(t, 1)
	reviewer := "owner/repo-1+review"
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Dynamic:  true,
			Resource: "https://github.com/owner/repo/pull/1",
			Outputs:  map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
			DoneWhen: &contract.DoneWhenState{
				Rounds:     1,
				LastAction: "escalate",
				Judges: map[string]*contract.DoneWhenJudge{
					"ac-met": {
						LeafID:          "ac-met",
						Action:          "request_changes",
						Reason:          "needs more work",
						Revision:        "sha1",
						ReviewerSession: reviewer,
						Relation:        string(domain.RelationSibling),
					},
				},
			},
		},
	})
	seedSession(t, store, reviewer, "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setParent(t, store, reviewer, "owner/repo-orchestrator")

	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Tasks["initial"].Outputs["revision"] = "sha2"
		return nil
	}); err != nil {
		t.Fatalf("update revision: %v", err)
	}

	// Simulate the reviewer's own log being unwritable: the auto-revival kick
	// delivery to it fails, while the work session's own review_required event
	// (published first) still succeeds.
	blockSessionEventsDir(t, store, reviewer)

	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err == nil {
		t.Fatal("TickSession with a blocked reviewer log, want error (delivery failure must not be swallowed)")
	}

	st := store.Get("owner/repo-1").Tasks["initial"]
	if st.DoneWhen.LastAutoRevivalRevision != "" {
		t.Fatalf("last_auto_revival_revision = %q, want empty: a failed delivery must not stamp the dedup marker", st.DoneWhen.LastAutoRevivalRevision)
	}
	if st.DoneWhen.LastAction != "escalate" {
		t.Fatalf("last_action = %q, want unchanged escalate: the failed tick must not have persisted the revived action either", st.DoneWhen.LastAction)
	}

	// Unblock the reviewer's log and retry: the same revision must now reach
	// the reviewer, and the dedup marker must be recorded.
	if err := os.Remove(filepath.Join(store.Dir(), "events", url.PathEscape(reviewer))); err != nil {
		t.Fatalf("unblock reviewer log: %v", err)
	}
	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("retry TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "review_required" || result.Actions[0].RevivalRevision != "sha2" {
		t.Fatalf("retry actions = %+v", result.Actions)
	}

	kicks, _, _, err := eventlog.NewStore(store.Dir()).List(reviewer, 0, event.Filter{Types: []string{event.TypeUserEmit}, Sources: []string{event.SourceTick}})
	if err != nil {
		t.Fatalf("list reviewer kicks: %v", err)
	}
	if len(kicks) != 1 {
		t.Fatalf("reviewer kicks after retry = %d, want exactly 1", len(kicks))
	}

	st = store.Get("owner/repo-1").Tasks["initial"]
	if st.DoneWhen.LastAutoRevivalRevision != "sha2" {
		t.Fatalf("last_auto_revival_revision = %q, want sha2 after the successful retry", st.DoneWhen.LastAutoRevivalRevision)
	}
}

func checkStatusOnlyConfig(t *testing.T, maxRounds int) *config.Config {
	t.Helper()
	return writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:    "work",
		scope: contract.TaskScopeSession,
		extra: fmt.Sprintf(`
requires = ["checks_status"]

[done_when]
all = [
  { check = "checks_status", eq = "SUCCESS" },
]

[done_when.budget]
max_rounds = %d

[outputs_schema]
type = "object"

[outputs_schema.properties]
checks_status = { type = "string", mutable = true }
revision = { type = "string", mutable = true }
`, maxRounds),
	}}, []nodeFixture{{id: "initial", uses: "work"}})
}

// blockEventsDir makes every eventlog.Append against store fail deterministically
// (a plain file sits where the events directory needs to be, so os.MkdirAll
// errors), to exercise CheckSession's publish-failure retry contract.
func blockEventsDir(t *testing.T, store *state.Store) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(store.Dir(), "events"), []byte("block"), 0o644); err != nil {
		t.Fatalf("blockEventsDir: %v", err)
	}
}

func unblockEventsDir(t *testing.T, store *state.Store) {
	t.Helper()
	if err := os.Remove(filepath.Join(store.Dir(), "events")); err != nil {
		t.Fatalf("unblockEventsDir: %v", err)
	}
}

// A publish failure must leave the tick marker unadvanced (no round bump, no
// LastAction) so a later, successful tick retries the same action rather
// than treating a failed delivery as handled.
func TestTickSession_PublishFailureLeavesMarkerUnadvancedForRetry(t *testing.T) {
	store := testStore(t)
	cfg := checkFixtureConfig(t)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
		},
	})
	blockEventsDir(t, store)

	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err == nil {
		t.Fatal("expected TickSession to fail when the event publish fails")
	}
	if check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen; check != nil && check.LastAction != "" {
		t.Fatalf("done_when state = %+v, want no marker persisted on publish failure", check)
	}

	unblockEventsDir(t, store)
	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("retry TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "kick" || result.Actions[0].Round != 1 {
		t.Fatalf("retry actions = %+v, want a fresh kick at round 1 (not skipped as already-handled)", result.Actions)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{event.TypeUserEmit}})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("kick events = %d, want exactly 1 (the failed attempt recorded nothing)", len(evs))
	}
}

// The same publish-before-persist contract applies to the terminal `done`
// push: a failed push to the parent must not be marked satisfied, so the next
// tick retries and the parent eventually receives exactly one `done` event.
func TestTickSession_SatisfiedPublishFailureAllowsRetry(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	blockEventsDir(t, store)

	if _, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"}); err == nil {
		t.Fatal("expected TickSession to fail when the terminal push fails")
	}
	if check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen; check != nil && check.LastAction == "satisfied" {
		t.Fatalf("done_when state = %+v, want not marked satisfied after a failed push", check)
	}

	unblockEventsDir(t, store)
	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("retry TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "satisfied" {
		t.Fatalf("retry actions = %+v, want satisfied", result.Actions)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDone}})
	if err != nil {
		t.Fatalf("list parent: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("done events on parent = %d, want 1 (retried once and delivered)", len(evs))
	}
}

// A terminal push whose target had to be woken, and whose wake failed, must
// still be recorded and reported satisfied — the wake failure surfaces as a
// tick warning, not a TickSession error, since the delivery itself
// succeeded.
func TestTickSession_SatisfiedWakeFailureIsWarnedNotFatal(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	// The parent has no run-scoped task and no frozen workflow: the terminal
	// push's best-effort wake (Up) will be attempted and fail.
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	result, err := TickSession(cfg, store, TickParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "satisfied" {
		t.Fatalf("actions = %+v, want satisfied despite the wake failure", result.Actions)
	}
	if len(result.Actions[0].Warnings) != 1 || !strings.Contains(result.Actions[0].Warnings[0], "wake") {
		t.Fatalf("warnings = %+v, want a wake-failure warning", result.Actions[0].Warnings)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDone}})
	if err != nil {
		t.Fatalf("list parent: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("done events on parent = %d, want 1 (delivered despite wake failure)", len(evs))
	}
}

// CheckSession keeps the same observation-only contract: same evaluation as tick, zero
// side effects. Calling it repeatedly on a kicked instance must not advance
// the round, append events, wake sessions, or push a terminal event even once
// the instance is actually satisfied.
func TestCheckSession_ObservationOnly(t *testing.T) {
	store := testStore(t)
	cfg := checkStatusOnlyConfig(t, 1)
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeSession,
			TaskID:  "work",
			Status:  contract.TaskStatusProduced,
			Dynamic: true,
			Outputs: map[string]any{"checks_status": "FAILURE", "revision": "sha1"},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	sessionsBefore := len(store.All())

	for i := range 3 {
		result, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
		if err != nil {
			t.Fatalf("CheckSession iteration %d: %v", i, err)
		}
		if len(result.Actions) != 1 || result.Actions[0].Action != "kick" {
			t.Fatalf("iteration %d actions = %+v, want kick reported (compat shape)", i, result.Actions)
		}
	}
	if check := store.Get("owner/repo-1").Tasks["initial"].DoneWhen; check != nil && (check.Rounds != 0 || check.LastAction != "") {
		t.Fatalf("done_when state = %+v, want untouched by observation-only check", check)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{})
	if err != nil {
		t.Fatalf("event list: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("events = %+v, want none published by check", evs)
	}
	if len(store.All()) != sessionsBefore {
		t.Fatalf("session count = %d, want unchanged %d (check must not spawn/wake sessions)", len(store.All()), sessionsBefore)
	}

	// Flip to a satisfied state and confirm check still never pushes `done`.
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Tasks["initial"].Outputs["checks_status"] = "SUCCESS"
		return nil
	}); err != nil {
		t.Fatalf("update outputs: %v", err)
	}
	result, err := CheckSession(cfg, store, CheckParams{SessionName: "owner/repo-1"})
	if err != nil {
		t.Fatalf("CheckSession after satisfying: %v", err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Action != "satisfied" {
		t.Fatalf("actions = %+v, want satisfied reported", result.Actions)
	}
	pushed, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDone}})
	if err != nil {
		t.Fatalf("list parent: %v", err)
	}
	if len(pushed) != 0 {
		t.Fatalf("done events on parent = %d, want 0 (check never pushes terminal events)", len(pushed))
	}
}
