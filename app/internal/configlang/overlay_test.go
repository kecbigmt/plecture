package configlang

import "testing"

func TestDiscoverWorkspaceOverlayLoadsOnlyWorkflowFragments(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "workflows/review.toml", `
[review_session]
kind = "workflow"

[[review_session.nodes]]
uses = "runtime"
`)
	// Outside workflows/: the allowlist means discovery never even looks
	// here, so this workspace_provider is simply not loaded, not an error.
	mustWrite(t, dir, "providers/worktree.toml", `
[worktree]
kind = "workspace_provider"
`)

	defs, err := DiscoverWorkspaceOverlay(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != "review_session" || defs[0].Kind != KindWorkflow {
		t.Fatalf("unexpected overlay defs: %v", defNames(defs))
	}
}

func TestDiscoverWorkspaceOverlayRejectsEffectDefinition(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "workflows/runtime.toml", `
[runtime]
kind  = "effect"
scope = "run"
`)
	_, err := DiscoverWorkspaceOverlay(dir)
	if err == nil {
		t.Fatal("expected an error: cloned content must not carry an effect definition")
	}
}

func TestDiscoverWorkspaceOverlaySkipsOtherWholeDefinitionKinds(t *testing.T) {
	for _, tc := range []struct {
		file, body string
	}{
		{"workflows/provider.toml", "[worktree]\nkind = \"workspace_provider\"\n"},
		{"workflows/observer.toml", "[issue_pr]\nkind = \"resource_observer\"\n"},
		{"workflows/delivery.toml", "[delivery]\nkind = \"channel\"\ntype = \"exec\"\n"},
	} {
		dir := t.TempDir()
		mustWrite(t, dir, tc.file, tc.body)
		defs, err := DiscoverWorkspaceOverlay(dir)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.file, err)
		}
		if len(defs) != 0 {
			t.Fatalf("%s: expected nothing loaded, got %v", tc.file, defNames(defs))
		}
	}
}

func TestDiscoverWorkspaceOverlayMissingDirIsNotAnError(t *testing.T) {
	defs, err := DiscoverWorkspaceOverlay("/nonexistent/does/not/exist/.plect")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected no defs, got %v", defNames(defs))
	}
}
