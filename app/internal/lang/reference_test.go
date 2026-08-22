package lang

import (
	"os"
	"testing"
)

// readFixtureBody reads a references/ fixture and strips its leading
// `# plect-fixture:` / `# reason:` comment header, mirroring what real
// config never carries.
func readFixtureBody(t *testing.T, rel string) []byte {
	t.Helper()
	return readConfigLanguageFixture(t, "references/"+rel)
}

// readConfigLanguageFixture reads a fixture from testdata/config-language/
// by its path relative to that directory, stripping the leading
// `# plect-fixture:` / `# reason:` header.
func readConfigLanguageFixture(t *testing.T, rel string) []byte {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(root + "/testdata/config-language/" + rel)
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return stripFixtureHeader(raw)
}

// stripFixtureHeader strips a fixture's `plect-fixture:`/`reason:` header —
// TOML comment lines for a definition document, HTML comment lines for a
// task document — the same way fixtureBody does for the schema-conformance
// pass, so a real config file never has to carry it.
func stripFixtureHeader(src []byte) []byte {
	return []byte(fixtureBody(string(src)))
}

func TestReferencesRelativeFixtureIsValid(t *testing.T) {
	defs, err := ParseDefinitionDocument("relative.toml", readFixtureBody(t, "relative.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	from := Ownership{IsPlugin: true, Alias: "official", Path: "github"}
	reg := NewRegistry([]PluginLayer{{Alias: "official", Path: "github", Defs: defs}}, nil)
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			if err := ResolveWorkflowRefs(d, from, reg); err != nil {
				t.Fatalf("unexpected error resolving %s: %v", d.ID, err)
			}
		}
	}
}

func TestReferencesQualifiedFixtureIsValid(t *testing.T) {
	defs, err := ParseDefinitionDocument("qualified.toml", readFixtureBody(t, "qualified.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// The catalog-qualified refs this fixture exercises name plugins a real
	// catalog would enable; a companion registry stands in for that catalog,
	// the way a fuller end-to-end load would supply it.
	reg := NewRegistry([]PluginLayer{
		{Alias: "official", Path: "github", Defs: []*Definition{defOf("worktree", KindWorkspaceProvider, nil)}},
		{Alias: "official", Path: "tmux", Defs: []*Definition{defOf("pane", KindEffect, nil)}},
		{Alias: "official", Path: "claude", Defs: []*Definition{
			defOf("runtime", KindEffect, nil),
			defOf("delivery", KindChannel, nil),
		}},
	}, defs)
	from := Ownership{}
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			if err := ResolveWorkflowRefs(d, from, reg); err != nil {
				t.Fatalf("unexpected error resolving %s: %v", d.ID, err)
			}
		}
	}
}

func TestReferencesCrossPluginFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("cross-plugin.toml", readFixtureBody(t, "cross-plugin.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	from := Ownership{IsPlugin: true, Alias: "official", Path: "github"}
	reg := NewRegistry([]PluginLayer{{Alias: "official", Path: "github", Defs: defs}}, nil)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, from, reg)
	assertDiagnostic(t, err, CodeRefCrossPlugin, LayerSemantic)
}

func TestReferencesAliasRequiredFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("alias-required.toml", readFixtureBody(t, "alias-required.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// "worktree" and "runtime" exist only under an enabled catalog plugin,
	// never in the user layer: that is what makes the omitted alias the
	// documented violation rather than a plain unknown reference.
	github := []*Definition{
		defOf("worktree", KindWorkspaceProvider, nil),
		defOf("runtime", KindEffect, nil),
	}
	reg := NewRegistry([]PluginLayer{{Alias: "official", Path: "github", Defs: github}}, defs)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, Ownership{}, reg)
	assertDiagnostic(t, err, CodeRefAliasRequired, LayerSemantic)
}

func TestReferencesDynamicUsesFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("dynamic-uses.toml", readFixtureBody(t, "dynamic-uses.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	reg := NewRegistry(nil, defs)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, Ownership{}, reg)
	assertDiagnostic(t, err, CodeRefDynamic, LayerStructural)
}

func TestReferencesUnknownRefFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("unknown-ref.toml", readFixtureBody(t, "unknown-ref.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	reg := NewRegistry(nil, defs)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, Ownership{}, reg)
	assertDiagnostic(t, err, CodeUnknownRef, LayerSemantic)
}

func TestReferencesWrongKindFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("wrong-kind.toml", readFixtureBody(t, "wrong-kind.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	reg := NewRegistry(nil, defs)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, Ownership{}, reg)
	assertDiagnostic(t, err, CodeKindMismatch, LayerSemantic)
}

func TestReferencesTaskInNodeFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("task-in-node.toml", readFixtureBody(t, "task-in-node.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// "review" is a task defined elsewhere in the same user layer — the
	// fixture is an excerpt that assumes it, per its own reason line.
	review, err := ParseTaskDocument("review.md", []byte("+++\n[review]\nkind = \"task\"\n+++\nbody\n"))
	if err != nil {
		t.Fatalf("unexpected error building the companion task: %v", err)
	}
	defs = append(defs, review)
	reg := NewRegistry(nil, defs)
	var workflow *Definition
	for _, d := range defs {
		if d.Kind == KindWorkflow {
			workflow = d
		}
	}
	err = ResolveWorkflowRefs(workflow, Ownership{}, reg)
	assertDiagnostic(t, err, CodeKindMismatch, LayerSemantic)
}

func TestWorkflowsDynamicWorkspaceProviderFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("dynamic-workspace-provider.toml",
		readConfigLanguageFixture(t, "workflows/dynamic-workspace-provider.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	reg := NewRegistry(nil, defs)
	err = ResolveWorkflowRefs(defs[0], Ownership{}, reg)
	assertDiagnostic(t, err, CodeRefDynamic, LayerStructural)
}

func TestWorkflowsProviderInputsDynamicFixtureIsRejected(t *testing.T) {
	defs, err := ParseDefinitionDocument("provider-inputs-dynamic.toml",
		readConfigLanguageFixture(t, "workflows/provider-inputs-dynamic.invalid.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// "worktree" only needs to exist and be the right kind: this fixture's
	// violation is workspace_provider_inputs carrying a computed value, not
	// the workspace_provider reference itself.
	reg := NewRegistry(nil, append(defs, defOf("worktree", KindWorkspaceProvider, nil)))
	err = ResolveWorkflowRefs(defs[0], Ownership{}, reg)
	assertDiagnostic(t, err, CodeValueTagSurface, LayerStructural)
}

func TestWorkflowsNodesFixtureIsValid(t *testing.T) {
	defs, err := ParseDefinitionDocument("nodes.toml", readConfigLanguageFixture(t, "workflows/nodes.toml"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// The fixture's own nodes reference "pane" and "official.codex.exec_runtime";
	// its event.channel references "official.codex.exec_delivery" — companion
	// definitions stand in for the plugins a real catalog would enable.
	reg := NewRegistry([]PluginLayer{
		{Alias: "official", Path: "codex", Defs: []*Definition{
			defOf("exec_runtime", KindEffect, nil),
			defOf("exec_delivery", KindChannel, nil),
		}},
	}, append(defs,
		defOf("pane", KindEffect, nil),
		defOf("initial_task", KindEffect, nil),
		defOf("okf_bundle", KindWorkspaceProvider, nil),
	))
	if err := ResolveWorkflowRefs(defs[0], Ownership{}, reg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConfigChannelsValid(t *testing.T) {
	cfg := &ConfigToml{Channels: []string{"notify"}}
	reg := NewRegistry(nil, []*Definition{defOf("notify", KindChannel, nil)})
	if err := ResolveConfigChannels(cfg, reg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConfigChannelsWrongKind(t *testing.T) {
	cfg := &ConfigToml{Channels: []string{"notify"}}
	reg := NewRegistry(nil, []*Definition{defOf("notify", KindEffect, nil)})
	err := ResolveConfigChannels(cfg, reg)
	assertDiagnostic(t, err, CodeKindMismatch, LayerSemantic)
}
