package service

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// addSetupWorkflow writes a second workflow alongside the one
// writeWorkflowFixture created: a one-node workflow whose own directory effect
// materializes a unique workdir. It lets a single test dispatch the same
// resource to two tools (claude vs codex).
func addSetupWorkflow(t *testing.T, cfg *config.Config, wfID, workdir string) {
	t.Helper()
	addFixtureWorkflow(t, cfg, wfID, "issue", "noop", "")
	attachWorkdir(t, cfg, wfID, taskFixture{id: wfID + "_dir", setup: mkdirScript(workdir)})
}

// TestCreate_DefaultTagSeparatesWorkflows is the ADR's core guarantee: with no
// explicit --tag, two tools dispatched at one resource land in distinct
// sessions because each session name carries its workflow id as the tag.
func TestCreate_DefaultTagSeparatesWorkflows(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "noop_base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg, "claude", filepath.Join(t.TempDir(), "claude"))
	addSetupWorkflow(t, cfg, "codex", filepath.Join(t.TempDir(), "codex"))

	url := "https://github.com/org/repo/issues/1"

	work, err := Create(cfg, store, CreateParams{URL: url, Workflow: "claude"})
	if err != nil {
		t.Fatalf("create claude: %v", err)
	}
	review, err := Create(cfg, store, CreateParams{URL: url, Workflow: "codex"})
	if err != nil {
		t.Fatalf("create codex: %v", err)
	}

	if work.SessionName != "org/repo-1+claude" {
		t.Errorf("claude session = %q, want org/repo-1+claude", work.SessionName)
	}
	if review.SessionName != "org/repo-1+codex" {
		t.Errorf("codex session = %q, want org/repo-1+codex", review.SessionName)
	}
	if work.SessionName == review.SessionName {
		t.Errorf("cross-tool sessions share the session %q; the default tag must separate them", work.SessionName)
	}
	if store.Get("org/repo-1+claude") == nil || store.Get("org/repo-1+codex") == nil {
		t.Fatal("both tagged sessions must persist")
	}
	if store.Get("org/repo-1") != nil {
		t.Error("untagged session must not be created when the default tag applies")
	}
}

// TestCreate_ExplicitTagOverridesDefault keeps --tag backward compatible: an
// explicit label wins over the workflow-id default.
func TestCreate_ExplicitTagOverridesDefault(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "noop_base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg, "codex", filepath.Join(t.TempDir(), "codex"))

	url := "https://github.com/org/repo/issues/2"
	res, err := Create(cfg, store, CreateParams{URL: url, Workflow: "codex", Tag: "review-initial"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.SessionName != "org/repo-2+review-initial" {
		t.Errorf("session = %q, want org/repo-2+review-initial (explicit tag wins)", res.SessionName)
	}
}

// TestUpCreateConvergeOnDefaultTag locks in the up/create symmetry: `plect up`
// with no tag derives the same workflow-id-tagged name Create does, so the
// auto-create reuses one session instead of forking a second.
func TestUpCreateConvergeOnDefaultTag(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "noop_base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg, "claude", filepath.Join(t.TempDir(), "claude"))

	url := "https://github.com/org/repo/issues/3"
	first, err := Up(cfg, store, UpParams{Identifier: url, Workflow: "claude"})
	if err != nil {
		t.Fatalf("first up: %v", err)
	}
	if first.SessionName != "org/repo-3+claude" {
		t.Fatalf("first up session = %q, want org/repo-3+claude", first.SessionName)
	}
	second, err := Up(cfg, store, UpParams{Identifier: url, Workflow: "claude"})
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	if second.SessionName != first.SessionName {
		t.Errorf("second up session = %q, want %q (must reuse, not fork)", second.SessionName, first.SessionName)
	}
	count := 0
	for name := range store.All() {
		if name == "org/repo-3+claude" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one session, got %d", count)
	}
}
