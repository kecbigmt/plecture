package service

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/domain"
	"github.com/cradel-dev/cradel/app/internal/state"
	"github.com/cradel-dev/cradel/app/internal/task"
	contract "github.com/cradel-dev/cradel/contracts/state"
)

// TestMain unsets SENNIT_SESSION_NAME before any test runs, so the suite stays
// hermetic when `go test` itself runs inside a sennit pane (this repo's own
// dev loop) rather than depending on the invoking shell being ambient-free.
// Tests that want to simulate a caller opt back in with t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("SENNIT_SESSION_NAME")
	os.Exit(m.Run())
}

func testStore(t *testing.T) *state.Store {
	t.Helper()
	return state.NewStore(t.TempDir())
}

// List ranges store.All() (a map), so without sorting its order is random and
// the web UI's auto-refresh reshuffles. Sessions must come back sorted by name.
func TestList_SortsTrackedByName(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	for _, n := range []string{"zzz/web-3", "aaa/web-1", "mmm/web-2"} {
		if err := store.Put(&domain.Session{Name: n, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}

	entries, err := List(&config.Config{}, store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var got []string
	for _, e := range entries {
		if e.SessionName == "aaa/web-1" || e.SessionName == "mmm/web-2" || e.SessionName == "zzz/web-3" {
			got = append(got, e.SessionName)
		}
	}
	want := []string{"aaa/web-1", "mmm/web-2", "zzz/web-3"}
	if !slices.Equal(got, want) {
		t.Errorf("tracked order = %v, want %v", got, want)
	}
}

func TestResolveSession_ByURL(t *testing.T) {
	store := testStore(t)
	// No session in store → should fail with workspace_not_found
	_, _, err := resolveSession(nil, store, "https://github.com/org/repo/issues/1")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestResolveSession_BySessionName(t *testing.T) {
	store := testStore(t)
	_, _, err := resolveSession(nil, store, "org/repo-1")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestResolveSession_UnknownIdentifier(t *testing.T) {
	store := testStore(t)
	_, _, err := resolveSession(nil, store, "https://example.test/org/repo")
	if err == nil {
		t.Fatal("expected error for an identifier with no state entry")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestResolveSessionName_BySessionName(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	if err := store.Put(&domain.Session{Name: "org/repo-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	name, err := ResolveSessionName(&config.Config{}, store, "org/repo-1")
	if err != nil {
		t.Fatalf("ResolveSessionName: %v", err)
	}
	if name != "org/repo-1" {
		t.Errorf("name = %q, want %q", name, "org/repo-1")
	}
}

func TestResolveSessionName_UnknownIdentifier(t *testing.T) {
	store := testStore(t)
	_, err := ResolveSessionName(&config.Config{}, store, "org/missing")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestValidTag(t *testing.T) {
	tests := []struct {
		tag   string
		valid bool
	}{
		{"review", true},
		{"debug-1", true},
		{"my_tag", true},
		{"ABC123", true},
		{"", true},     // empty tag is allowed (means no tag)
		{"a+b", false}, // + is the separator
		{"a/b", false}, // path separator
		{"a b", false}, // space
		{"a:b", false}, // tmux conflict
		{"日本語", false}, // multibyte sample: exercises rejection of non-ASCII tags
	}
	for _, tt := range tests {
		if tt.tag == "" {
			continue // empty tag skips validation
		}
		got := validTag.MatchString(tt.tag)
		if got != tt.valid {
			t.Errorf("validTag.MatchString(%q) = %v, want %v", tt.tag, got, tt.valid)
		}
	}
}

func TestStatus_UnknownIdentifier(t *testing.T) {
	cfg := &config.Config{}
	store := testStore(t)
	_, err := Status(cfg, store, "https://example.test/org/repo")
	if err == nil {
		t.Fatal("expected error for an identifier with no state entry")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestStatus_SessionNameNotFound(t *testing.T) {
	cfg := &config.Config{}
	store := testStore(t)
	_, err := Status(cfg, store, "owner/repo-123")
	if err == nil {
		t.Fatal("expected error for unknown session name")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

func TestStatus_DestroyedSessionReturnsTombstone(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "envfile", scope: "session", setup: "true", cleanup: "true"},
		},
		[]nodeFixture{{id: "envfile"}},
	)
	sessionName := "org/repo-1"
	seedSession(t, store, sessionName, "org/repo", 1, "default", map[string]*contract.TaskState{
		"envfile": {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"path": "/tmp/env"},
		},
	})
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	result, err := Status(cfg, store, sessionName)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !result.Destroyed {
		t.Fatal("expected Destroyed = true")
	}
	if result.DestroyedAt.IsZero() {
		t.Error("expected DestroyedAt to be set")
	}
	if result.Identity.SessionName != sessionName {
		t.Errorf("SessionName = %q, want %q", result.Identity.SessionName, sessionName)
	}
	found := false
	for _, tv := range result.Work {
		if tv.Instance == "envfile" && tv.Outputs["path"] == "/tmp/env" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected envfile task outputs preserved in tombstone-backed Status, got %+v", result.Work)
	}
}

func TestStatus_DestroyedSessionWithoutTombstoneStillErrors(t *testing.T) {
	cfg := &config.Config{}
	store := testStore(t)
	_, err := Status(cfg, store, "owner/repo-999")
	if err == nil {
		t.Fatal("expected error for unknown session with no tombstone")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrWorkspaceNotFound {
		t.Fatalf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

// Status projects the session tree (parent + children, derived from the parent
// pointers Subtree walks), so the web detail can link across the tree.
func TestStatus_ProjectsTree(t *testing.T) {
	cfg := &config.Config{}
	store := testStore(t)
	now := time.Now()
	wt := t.TempDir()
	for _, n := range []string{"org/repo-1", "org/repo-2", "org/repo-3"} {
		if err := store.Put(&domain.Session{
			Name: n, CreatedAt: now, UpdatedAt: now,
			WorktreePath: wt,
		}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	setParent(t, store, "org/repo-2", "org/repo-1")
	setParent(t, store, "org/repo-3", "org/repo-1")

	root, err := Status(cfg, store, "org/repo-1")
	if err != nil {
		t.Fatalf("Status(root): %v", err)
	}
	if root.Identity.ParentSession != "" {
		t.Errorf("root ParentSession = %q, want empty", root.Identity.ParentSession)
	}
	if want := []string{"org/repo-2", "org/repo-3"}; !slices.Equal(root.Identity.Children, want) {
		t.Errorf("root Children = %v, want %v", root.Identity.Children, want)
	}

	child, err := Status(cfg, store, "org/repo-2")
	if err != nil {
		t.Fatalf("Status(child): %v", err)
	}
	if child.Identity.ParentSession != "org/repo-1" {
		t.Errorf("child ParentSession = %q, want org/repo-1", child.Identity.ParentSession)
	}
	if len(child.Identity.Children) != 0 {
		t.Errorf("leaf Children = %v, want none", child.Identity.Children)
	}
}

func TestSetConversation(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{
		Name:      "owner/repo-1",
		CreatedAt: now,
		UpdatedAt: now,
	})

	conv := &domain.Conversation{
		Source:   "Slack",
		URL:      "https://exampleorg.slack.com/archives/C123/p456",
		Metadata: map[string]string{"thread_ts": "456", "channel_id": "C123"},
	}
	err := SetConversation(nil, store, "owner/repo-1", conv)
	if err != nil {
		t.Fatalf("SetConversation() error: %v", err)
	}

	got := store.Get("owner/repo-1")
	if got.Conversation == nil {
		t.Fatal("Conversation should be set")
	}
	if got.Conversation.Source != "Slack" {
		t.Errorf("Source = %q, want %q", got.Conversation.Source, "Slack")
	}
	if got.Conversation.URL != "https://exampleorg.slack.com/archives/C123/p456" {
		t.Errorf("URL = %q, want %q", got.Conversation.URL, "https://exampleorg.slack.com/archives/C123/p456")
	}
	if got.Conversation.Metadata["thread_ts"] != "456" {
		t.Errorf("Metadata[thread_ts] = %q, want %q", got.Conversation.Metadata["thread_ts"], "456")
	}
	if got.Conversation.Metadata["channel_id"] != "C123" {
		t.Errorf("Metadata[channel_id] = %q, want %q", got.Conversation.Metadata["channel_id"], "C123")
	}
}

func TestSetConversation_SessionNotFound(t *testing.T) {
	store := testStore(t)
	conv := &domain.Conversation{Source: "Slack", URL: "https://example.com"}
	err := SetConversation(nil, store, "owner/repo-999", conv)
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

// SetConversation is a per-session write path (hook scripts call it to attach
// a Slack thread), so it must honor SessionGuard like Create/Destroy/EventPublish
// — otherwise a guarded orchestrator could still relabel another owner's
// session's conversation.
func TestSetConversation_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{Name: "exampleorg/repo-26", CreatedAt: now, UpdatedAt: now})
	cfg := &config.Config{SessionGuard: "^acme/"}

	conv := &domain.Conversation{Source: "Slack", URL: "https://example.com"}
	err := SetConversation(cfg, store, "exampleorg/repo-26", conv)
	if err == nil {
		t.Fatal("expected session-guard rejection for cross-owner conversation write")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Errorf("want ErrRepoNotAllowed, got %v", err)
	}
	if store.Get("exampleorg/repo-26").Conversation != nil {
		t.Error("blocked SetConversation must not mutate the session")
	}
}

func TestSetMessage(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{
		Name:      "owner/repo-1",
		CreatedAt: now,
		UpdatedAt: now,
	})

	if err := SetMessage(nil, store, "owner/repo-1", "working"); err != nil {
		t.Fatalf("SetMessage() error: %v", err)
	}

	got := store.Get("owner/repo-1")
	if got.Message == nil {
		t.Fatal("Message should be set")
	}
	if got.Message.Text != "working" {
		t.Errorf("Text = %q, want %q", got.Message.Text, "working")
	}
	if got.Message.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}

	// Empty text unsets the message rather than persisting a blank line.
	if err := SetMessage(nil, store, "owner/repo-1", ""); err != nil {
		t.Fatalf("SetMessage(\"\") error: %v", err)
	}
	if store.Get("owner/repo-1").Message != nil {
		t.Error("empty text should clear Message")
	}
}

func TestSetMessage_SessionNotFound(t *testing.T) {
	store := testStore(t)
	err := SetMessage(nil, store, "owner/repo-999", "working")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	svcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if svcErr.Code != ErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", svcErr.Code, ErrWorkspaceNotFound)
	}
}

// SetMessage is a per-session write path (hook scripts call it on every turn
// boundary), so it must honor SessionGuard like SetConversation —
// otherwise a guarded orchestrator could still relabel another owner's
// session's status.
func TestSetMessage_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{Name: "exampleorg/repo-26", CreatedAt: now, UpdatedAt: now})
	cfg := &config.Config{SessionGuard: "^acme/"}

	err := SetMessage(cfg, store, "exampleorg/repo-26", "working")
	if err == nil {
		t.Fatal("expected session-guard rejection for cross-owner message write")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Errorf("want ErrRepoNotAllowed, got %v", err)
	}
	if store.Get("exampleorg/repo-26").Message != nil {
		t.Error("blocked SetMessage must not mutate the session")
	}
}

func TestApplyDisplay_OverridesFromOutputs(t *testing.T) {
	workflows := map[string]config.WorkflowFile{
		"wf": {
			ID: "wf",
			Display: map[string]string{
				"title":  "{{.Workflow.outputs.title}}",
				"status": "{{.Workflow.outputs.pr_state}}",
			},
		},
	}
	s := &domain.Session{
		Name:     "org/repo-1",
		Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			contract.WorkflowPseudoNodeID: {
				Status:  contract.TaskStatusProduced,
				Outputs: map[string]any{"title": "Fix the bug", "pr_state": "open"},
			},
		},
	}
	cached := cachedInfo{Title: "from-cache", DisplayStatus: "cache-status"}
	applyDisplay(workflows, s, &cached)
	if cached.Title != "Fix the bug" {
		t.Errorf("Title = %q, want display override", cached.Title)
	}
	if cached.DisplayStatus != "open" {
		t.Errorf("GitHubStatus = %q, want open", cached.DisplayStatus)
	}
}

func TestApplyDisplay_EmptyRenderKeepsFallback(t *testing.T) {
	workflows := map[string]config.WorkflowFile{
		"wf": {ID: "wf", Display: map[string]string{"title": "{{.Workflow.outputs.title}}"}},
	}
	// No outputs at all → template renders empty → prior title survives.
	s := &domain.Session{Name: "org/repo-2", Workflow: "wf"}
	cached := cachedInfo{Title: "from-cache"}
	applyDisplay(workflows, s, &cached)
	if cached.Title != "from-cache" {
		t.Errorf("Title = %q, want unchanged fallback", cached.Title)
	}
}

func TestTaskViews_EvaluatesDoneWhenPerRuntimeTaskOutputs(t *testing.T) {
	success := "SUCCESS"
	defs := map[string]config.TaskDefinition{
		"review": {
			ID: "review",
			DoneWhen: &config.DoneWhen{All: []config.DoneWhenLeaf{
				{Check: "checks_status", Eq: &success},
			}},
		},
	}
	s := &domain.Session{
		Name: "org/repo-1",
		Tasks: map[string]*contract.TaskState{
			"review#1": {
				Scope:   contract.TaskScopeSession,
				Status:  contract.TaskStatusProduced,
				TaskID:  "review",
				Dynamic: true,
				Seq:     1,
				Outputs: map[string]any{"checks_status": "SUCCESS", "pr_state": "open"},
			},
			"review#2": {
				Scope:   contract.TaskScopeSession,
				Status:  contract.TaskStatusProduced,
				TaskID:  "review",
				Dynamic: true,
				Seq:     2,
				Outputs: map[string]any{"checks_status": "FAILURE", "pr_state": "open"},
			},
		},
	}

	views := taskViews(defs, s, map[string]*domain.Session{s.Name: s})
	if len(views) != 2 {
		t.Fatalf("len(taskViews) = %d, want 2", len(views))
	}
	if views[0].Instance != "review#1" || views[0].DoneWhen == nil || views[0].DoneWhen.Overall != task.DoneSatisfied {
		t.Fatalf("review#1 view = %+v, want satisfied", views[0])
	}
	if views[1].Instance != "review#2" || views[1].DoneWhen == nil || views[1].DoneWhen.Overall != task.DoneUnsatisfied {
		t.Fatalf("review#2 view = %+v, want unsatisfied", views[1])
	}
	if got := views[1].DoneWhen.Leaves[0].Value; got != "FAILURE" {
		t.Errorf("review#2 checks_status value = %q, want FAILURE", got)
	}
}

func TestShowAndListExposePerRuntimeTaskDoneWhen(t *testing.T) {
	store := testStore(t)
	extra := `
[[done_when.all]]
check = "checks_status"
eq = "SUCCESS"
`
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "review", scope: "session", extra: extra}},
		[]nodeFixture{{id: "review"}})
	seedSession(t, store, "org/repo-1", "org/repo", 1, "wf", map[string]*contract.TaskState{
		"review#1": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "review", Dynamic: true, Seq: 1, Outputs: map[string]any{"checks_status": "SUCCESS"}},
		"review#2": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "review", Dynamic: true, Seq: 2, Outputs: map[string]any{"checks_status": "FAILURE"}},
	})

	status, err := Status(cfg, store, "org/repo-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	assertRuntimeDoneWhenWork(t, status.Work)

	list, err := List(cfg, store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List returned no entries")
	}
	assertRuntimeDoneWhenViews(t, list[0].Tasks)
}

func assertRuntimeDoneWhenViews(t *testing.T, views []TaskInstanceView) {
	t.Helper()
	if len(views) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(views))
	}
	if views[0].Instance != "review#1" || views[0].DoneWhen == nil || views[0].DoneWhen.Overall != task.DoneSatisfied {
		t.Fatalf("review#1 view = %+v, want satisfied", views[0])
	}
	if views[1].Instance != "review#2" || views[1].DoneWhen == nil || views[1].DoneWhen.Overall != task.DoneUnsatisfied {
		t.Fatalf("review#2 view = %+v, want unsatisfied", views[1])
	}
}

func assertRuntimeDoneWhenWork(t *testing.T, work []StatusTask) {
	t.Helper()
	if len(work) != 2 {
		t.Fatalf("len(work) = %d, want 2", len(work))
	}
	if work[0].Instance != "review#1" || work[0].DoneWhen == nil || work[0].DoneWhen.Overall != task.DoneSatisfied {
		t.Fatalf("review#1 work = %+v, want satisfied", work[0])
	}
	if work[1].Instance != "review#2" || work[1].DoneWhen == nil || work[1].DoneWhen.Overall != task.DoneUnsatisfied {
		t.Fatalf("review#2 work = %+v, want unsatisfied", work[1])
	}
}
