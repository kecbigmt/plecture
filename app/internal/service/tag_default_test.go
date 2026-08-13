package service

import (
	"os"
	"path/filepath"
	"testing"
)

// addSetupWorkflow writes a second provider-backed workflow alongside the one
// writeWorkflowFixture created: a one-node workflow file plus a provider TOML
// (the github resolver + a setup that materializes a unique workdir). It lets a
// single test dispatch the same resource to two tools (claude vs codex).
func addSetupWorkflow(t *testing.T, baseDir, wfID, workdir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(baseDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "provider = \"" + wfID + "\"\n[[nodes]]\nid = \"noop\"\n"
	if err := os.WriteFile(filepath.Join(baseDir, "workflows", wfID+".toml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := "setup = '''\nmkdir -p " + workdir + "\necho '{\"workdir\":\"" + workdir + "\"}'\n'''\n" + githubResolver
	if err := os.WriteFile(filepath.Join(baseDir, "providers", wfID+".toml"), []byte(prov), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCreate_DefaultTagSeparatesWorkflows is the ADR's core guarantee: with no
// explicit --tag, two tools dispatched at one resource land in distinct
// sessions because each session name carries its workflow id as the tag.
func TestCreate_DefaultTagSeparatesWorkflows(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "noop-base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg.BaseDir, "claude", filepath.Join(t.TempDir(), "claude"))
	addSetupWorkflow(t, cfg.BaseDir, "codex", filepath.Join(t.TempDir(), "codex"))

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
	if work.WorkdirPath == review.WorkdirPath {
		t.Errorf("cross-tool sessions share a session %q; the default tag must separate them", work.WorkdirPath)
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
	cfg := writeWorkflowFixture(t, t.TempDir(), "noop-base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg.BaseDir, "codex", filepath.Join(t.TempDir(), "codex"))

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
	cfg := writeWorkflowFixture(t, t.TempDir(), "noop-base",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addSetupWorkflow(t, cfg.BaseDir, "claude", filepath.Join(t.TempDir(), "claude"))

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
