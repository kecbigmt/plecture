package service

import (
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestDispatchResource_AutoUniqueMatch(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})

	disp, matched, err := dispatchResource(cfg, "", "", "https://github.com/org/repo/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected resolver match")
	}
	if disp.Name != "org/repo-42" {
		t.Errorf("Name = %q, want org/repo-42", disp.Name)
	}
	if disp.Workflow.ID != "gh" {
		t.Errorf("Workflow = %q, want gh", disp.Workflow.ID)
	}
}

func TestDispatchResource_NoMatchFallsThrough(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})

	_, matched, err := dispatchResource(cfg, "", "", "https://jira.example.com/browse/PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("non-matching resource must not dispatch")
	}
}

func TestDispatchResource_AmbiguousIsError(t *testing.T) {
	cfg := writeWorkflowFixture(t, "a",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addFixtureWorkflow(t, cfg, "b", "issue", "noop", "")

	_, _, err := dispatchResource(cfg, "", "", "https://github.com/org/repo/issues/1")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "--workflow") || !strings.Contains(err.Error(), "(a, b)") {
		t.Errorf("error should list both candidates and suggest --workflow: %v", err)
	}
}

func TestDispatchResource_AutoSelectFalseSkippedUnlessExplicit(t *testing.T) {
	cfg := writeWorkflowFixture(t, "claude",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addFixtureWorkflow(t, cfg, "codex", "issue", "noop", "auto_select = false\n")
	url := "https://github.com/org/repo/issues/1"

	disp, matched, err := dispatchResource(cfg, "", "", url)
	if err != nil {
		t.Fatal(err)
	}
	if !matched || disp.Workflow.ID != "claude" {
		t.Fatalf("auto dispatch = matched %v workflow %q, want claude", matched, disp.Workflow.ID)
	}

	disp, matched, err = dispatchResource(cfg, "", "codex", url)
	if err != nil {
		t.Fatal(err)
	}
	if !matched || disp.Workflow.ID != "codex" {
		t.Fatalf("explicit dispatch = matched %v workflow %q, want codex", matched, disp.Workflow.ID)
	}
}

func TestDispatchResource_FlagForAnotherResourceTypeIsError(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeResourceDoc(t, cfg, "other", `[other]
kind  = "resource"
match = '^other://'
name  = { expr = "'o'" }

[other.observe]
type   = "shell"
script = "echo '{}'"
`)
	addFixtureWorkflow(t, cfg, "elsewhere", "other", "noop", "")

	_, _, err := dispatchResource(cfg, "", "elsewhere", "https://github.com/org/repo/issues/1")
	if err == nil {
		t.Fatal("expected a resource-type mismatch error for explicit --workflow")
	}
	if !strings.Contains(err.Error(), "accepts resource") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestDispatchResource_UnrecognizedIdentifierIsNotDispatched(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})

	_, matched, err := dispatchResource(cfg, "", "gh", "my-session")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("an identifier no resource recognizes must be left to the caller as a session name")
	}
}

func TestDispatchResource_NoWorkflowAcceptingResourceIsError(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addWorkflowFields(t, cfg, "gh", "auto_select = false\n")

	_, _, err := dispatchResource(cfg, "", "", "https://github.com/org/repo/issues/1")
	if err == nil || !strings.Contains(err.Error(), "no workflow is selected for resource") || !strings.Contains(err.Error(), "--workflow gh") {
		t.Fatalf("err = %v, want a no-workflow error naming gh as a --workflow candidate", err)
	}
}

func TestDispatchResource_NoWorkflowOfTheTypeListsWhatTheWorkflowsAccept(t *testing.T) {
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	writeResourceDoc(t, cfg, "other", `[other]
kind  = "resource"
match = '^other://'
name  = { expr = "'o'" }

[other.observe]
type   = "shell"
script = "echo '{}'"
`)

	_, _, err := dispatchResource(cfg, "", "", "other://x")
	if err == nil || !strings.Contains(err.Error(), `no workflow accepts resource "other"`) || !strings.Contains(err.Error(), `gh (resource "issue")`) {
		t.Fatalf("err = %v, want a no-workflow error listing gh and the type it accepts", err)
	}
}

func TestValidateSessionName(t *testing.T) {
	for _, ok := range []string{"org/repo-1", "org/repo-1+review", "my_session", "a"} {
		if err := validateSessionName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "with:colon", "with.dot", "-leading-dash", "https://github.com/x"} {
		if err := validateSessionName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestCreate_ResolverPath(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "gh", workdir)

	url := "https://github.com/org/repo/issues/42"
	result, err := Create(cfg, store, CreateParams{URL: url})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.SessionName != "org/repo-42" {
		t.Errorf("SessionName = %q, want org/repo-42", result.SessionName)
	}
	s := store.Get("org/repo-42")
	if s.ResourceID != url || s.Alias != url {
		t.Errorf("ResourceID/Alias = %q/%q, want both %q", s.ResourceID, s.Alias, url)
	}
}

func TestCreate_ResourceAllowlistBlocks(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	cfg.ResourceAllowlist = []string{`^https://github\.com/allowed-org/`}

	_, err := Create(cfg, store, CreateParams{URL: "https://github.com/evil-org/repo/issues/1"})
	if err == nil {
		t.Fatal("expected allowlist rejection")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Errorf("want ErrRepoNotAllowed, got %v", err)
	}
}

func TestCreate_SessionGuardBlocksCrossOwner(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	// The orchestrator pane exports this guard; it matches resolved names like
	// "acme/repo-1" but not "exampleorg/repo-26".
	cfg.SessionGuard = "^acme/"

	// acme's orchestrator must not be able to dispatch exampleorg work.
	_, err := Create(cfg, store, CreateParams{URL: "https://github.com/exampleorg/repo/issues/26"})
	if err == nil {
		t.Fatal("expected session-guard rejection for cross-owner dispatch")
	}
	svcErr, ok := err.(*Error)
	if !ok || svcErr.Code != ErrRepoNotAllowed {
		t.Errorf("want ErrRepoNotAllowed, got %v", err)
	}
}

func TestResolveSession_ByAliasAndResolver(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "gh", workdir)

	url := "https://github.com/org/repo/issues/77"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatal(err)
	}

	// By name.
	if name, _, err := resolveSession(cfg, store, "org/repo-77"); err != nil || name != "org/repo-77" {
		t.Errorf("by name: %v / %q", err, name)
	}
	// By alias (the original input is the create-time alias).
	if name, _, err := resolveSession(cfg, store, url); err != nil || name != "org/repo-77" {
		t.Errorf("by alias: %v / %q", err, name)
	}
}

func TestResolveSession_ResolverDerivationAfterRuleChange(t *testing.T) {
	// Session created under an old rule: alias differs from today's input.
	store := testStore(t)
	seedSession(t, store, "org/repo-5", "org/repo", 5, "gh", nil)

	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})

	// seedSession sets URL to .../issues/1 — look up via a never-stored URL
	// shape that the resolver derives to the right name. Note: pull URL for
	// the same number maps to the same session id.
	name, _, err := resolveSession(cfg, store, "https://github.com/org/repo/pull/5")
	if err != nil {
		t.Fatalf("resolver derivation lookup: %v", err)
	}
	if name != "org/repo-5" {
		t.Errorf("name = %q", name)
	}
}

func TestUp_ResolverAutoCreate(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "gh", workdir)

	url := "https://github.com/org/repo/issues/88"
	result, err := Up(cfg, store, UpParams{Identifier: url})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if result.SessionName != "org/repo-88" {
		t.Errorf("SessionName = %q, want org/repo-88", result.SessionName)
	}
	s := store.Get("org/repo-88")
	if st := s.Nodes[workdirEffectID]; st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("auto-create did not run the session nodes: %+v", st)
	}
}

// Up must surface dispatch errors exactly as Create does — falling through
// to the legacy path on ambiguity would let explicit and auto-created paths
// disagree about the same resource.
func TestUp_AmbiguousResolverDispatchIsError(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "a",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	addFixtureWorkflow(t, cfg, "b", "issue", "noop", "")

	_, err := Up(cfg, store, UpParams{Identifier: "https://github.com/org/repo/issues/1"})
	if err == nil {
		t.Fatal("expected ambiguity error from Up")
	}
	if !strings.Contains(err.Error(), "--workflow") {
		t.Errorf("error should suggest --workflow: %v", err)
	}
}

// Every create path must satisfy the session identity contract.
func TestCreate_SetsResourceIDAndAlias(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "bridge",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "bridge", workdir)
	url := "https://github.com/org/repo/issues/55"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatal(err)
	}
	s := store.Get("org/repo-55")
	if s.ResourceID != url || s.Alias != url {
		t.Errorf("ResourceID/Alias = %q/%q, want %q", s.ResourceID, s.Alias, url)
	}
}

// `plect cd` rides the same state v3 lookup contract as show/down/destroy:
// resolver-derived resource ids and aliases must resolve.
func TestWorkdir_ResolverAndAliasLookup(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, "gh",
		[]taskFixture{{id: "noop", scope: "session", setup: "echo '{}'"}},
		[]nodeFixture{{id: "noop"}})
	attachWorkdirDir(t, cfg, "gh", workdir)

	url := "https://github.com/org/repo/issues/66"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"org/repo-66", url} {
		got, err := Workdir(cfg, store, id)
		if err != nil {
			t.Errorf("Workdir(%q): %v", id, err)
			continue
		}
		if got != workdir {
			t.Errorf("Workdir(%q) = %q, want %q", id, got, workdir)
		}
	}

	if _, err := Workdir(cfg, store, "no-such-session"); err == nil {
		t.Error("unknown identifier should error")
	}
}
