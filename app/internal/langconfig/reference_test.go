package langconfig

import (
	"os"
	"strings"
	"testing"
)

// readFixtureBody reads a references/ fixture and strips its leading
// `# plect-fixture:` / `# reason:` comment header, mirroring what real
// config never carries.
func readFixtureBody(t *testing.T, rel string) []byte {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(root + "/testdata/config-language/references/" + rel)
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return stripFixtureHeader(raw)
}

func stripFixtureHeader(src []byte) []byte {
	s := string(src)
	for strings.HasPrefix(s, "#") {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			return []byte("")
		}
		s = s[nl+1:]
	}
	return []byte(strings.TrimLeft(s, "\n"))
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
