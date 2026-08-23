package lang

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixturePlugin stands in for the plugin that would contain the conformance
// corpus: the fixtures are written as plugin-owned config, so a bare bin name
// resolves against this executable set and a bare definition reference
// against this namespace.
const (
	fixtureAlias = "fixtures"
	fixturePath  = "corpus"
)

// fixtureExecutables are the executables the corpus's valid fixtures name.
// The corpus carries no plugin.toml, so the containing plugin's manifest is
// declared here instead; okf-goal-absent is deliberately not among them.
var fixtureExecutables = []string{
	"agent-runtime",
	"agent-runtime-send",
	"codex-agent-activity",
	"codex-exec-enqueue",
	"codex-exec-worker",
	"gh-guard",
	"github-issue-pr",
	"github-watcher",
	"github-worktree",
	"okf-bundle",
	"okf-goal",
	"slack-post",
}

// fixtureContext names the corpus files declaring the definitions the task
// fixtures reference but do not themselves carry — the observer each one is
// written for, and the workflow a chain names. They are ordinary fixtures;
// this list is only what stands in for the layer a real load would have
// discovered around them.
var fixtureContext = []string{
	"observers/issue-pr.toml",
	"observers/observe-finalize.toml",
	"workflows/nodes.toml",
}

// nativeDeferred names every fixture whose documented diagnostic a later
// implementation slice produces, with the check that is still missing. A
// fixture leaves this map when that check lands; nothing else may be
// skipped.
var nativeDeferred = map[string]string{
	"nesting/computed-output-mutable.invalid.toml": "the nesting joint's mutability agreement",
	"nesting/cycle.invalid.toml":                   "nesting chain resolution",
	"nesting/projection-mismatch.invalid.toml":     "the nesting joint's type agreement",
	"references/alias-required.invalid.toml":       "reference resolution across enabled plugin layers",
	"references/cross-plugin.invalid.toml":         "reference resolution across enabled plugin layers",
	"references/duplicate-id.invalid.toml":         "duplicate-id detection across a layer's files",
	"references/unknown-ref.invalid.toml":          "reference resolution across enabled plugin layers",
	"references/wrong-kind.invalid.toml":           "reference resolution across enabled plugin layers",
	"references/task-in-node.invalid.toml":         "reference resolution across enabled plugin layers",
}

// instantiationCase is the binding one layer=instantiation fixture needs to
// be exercised: which resource the instance is created against, and — for a
// fixture whose expectation is a failed observation — what the observer
// reports when asked.
type instantiationCase struct {
	resourceID string
	observeErr string
}

// nativeInstantiation names every fixture whose expectation only a binding
// can break, so TestNativeInstantiationFixtures asserts it instead of the
// load pass. The corpus states the rule; the binding that breaks it is not
// something a document can carry, so it is declared here.
var nativeInstantiation = map[string]instantiationCase{
	"tasks/resource-mismatch.invalid.md":    {resourceID: "local-okf://acme/goals/ship.md"},
	"tasks/first-observe-failed.invalid.md": {resourceID: "local-okf://acme/goals/ship.md", observeErr: "goal file does not parse"},
}

// TestNativeConformanceFixtures is the semantic half of this package's
// conformance harness. Every definition document and task document under
// testdata/config-language/ is loaded by this package's own parsers and
// validators, and the diagnostic it produces must be exactly the one its
// expectation header documents — where TestConformanceFixtures asserts what
// the structural schema accepts, this asserts what the implementation does.
func TestNativeConformanceFixtures(t *testing.T) {
	fixtureRoot := filepath.Join(repoRoot(t), "testdata", "config-language")
	var paths []string
	err := filepath.Walk(fixtureRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) == "README.md" {
			return err
		}
		if strings.HasSuffix(path, ".toml") || strings.HasSuffix(path, ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)

	context := fixtureContextDefs(t, fixtureRoot)

	seenDeferred := map[string]bool{}
	for _, path := range paths {
		rel := filepath.ToSlash(mustRel(t, fixtureRoot, path))
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			exp, err := parseFixtureHeader(string(raw))
			if err != nil {
				t.Fatalf("bad fixture header: %v", err)
			}
			if exp.Entry != "definitions" && exp.Entry != "task" {
				return // a reserved root file or a manifest, not a definition
			}
			if reason, deferred := nativeDeferred[rel]; deferred {
				seenDeferred[rel] = true
				t.Skipf("deferred: %s", reason)
			}
			if _, bound := nativeInstantiation[rel]; bound {
				t.Skip("asserted by TestNativeInstantiationFixtures, which supplies the binding")
			}

			got := nativeLoad(rel, exp.Entry, fixtureBody(string(raw)), context)
			switch exp.Result {
			case "valid", "accepted-invalid":
				if got != nil {
					t.Errorf("expected this to load (%s), but: %v", exp.Result, got)
				}
			default:
				var d *Diagnostic
				if !errors.As(got, &d) {
					t.Fatalf("expected %s, got %v", exp.Diagnostic, got)
				}
				if string(d.Code) != exp.Diagnostic {
					t.Errorf("expected %s, got %s: %v", exp.Diagnostic, d.Code, got)
				}
				if string(d.Layer) != exp.Layer {
					t.Errorf("%s: expected layer %s, got %s", d.Code, exp.Layer, d.Layer)
				}
			}
		})
	}
	for rel := range nativeDeferred {
		if !seenDeferred[rel] {
			t.Errorf("nativeDeferred names %s, which is not a definition fixture in the corpus", rel)
		}
	}
}

// TestNativeInstantiationFixtures asserts the corpus's layer=instantiation
// fixtures, which a load cannot break on its own: the document is valid, and
// what fails is binding it to a resource.
func TestNativeInstantiationFixtures(t *testing.T) {
	fixtureRoot := filepath.Join(repoRoot(t), "testdata", "config-language")
	context := fixtureContextDefs(t, fixtureRoot)
	for rel, binding := range nativeInstantiation {
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			exp, err := parseFixtureHeader(string(raw))
			if err != nil {
				t.Fatalf("bad fixture header: %v", err)
			}
			def, err := ParseTaskDocument(rel, []byte(fixtureBody(string(raw))))
			if err != nil {
				t.Fatalf("the document itself loads: %v", err)
			}
			v := Validation{
				From:        Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath},
				Executables: NewExecutableRegistry(PluginExecutables{Alias: fixtureAlias, Path: fixturePath, Names: fixtureExecutables}),
			}
			registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(append([]*Definition(nil), context...), def)}}, nil)
			if err := v.ValidateDefinition(def); err != nil {
				t.Fatalf("the document itself loads: %v", err)
			}
			if err := v.ValidateTaskContracts(def, registry); err != nil {
				t.Fatalf("the document itself loads: %v", err)
			}
			observe := func(*Definition, string) (map[string]any, error) {
				if binding.observeErr != "" {
					return nil, errors.New(binding.observeErr)
				}
				return map[string]any{}, nil
			}
			_, err = v.Instantiate(def, registry, binding.resourceID, observe)
			var d *Diagnostic
			if !errors.As(err, &d) {
				t.Fatalf("expected %s, got %v", exp.Diagnostic, err)
			}
			if string(d.Code) != exp.Diagnostic {
				t.Errorf("expected %s, got %s: %v", exp.Diagnostic, d.Code, err)
			}
			if string(d.Layer) != exp.Layer {
				t.Errorf("%s: expected layer %s, got %s", d.Code, exp.Layer, d.Layer)
			}
		})
	}
}

// nativeLoad runs one fixture through the load pipeline this package
// implements: parse, then value, expression, action, and executable
// validation, then the plan-level capability check.
func nativeLoad(path, entry, body string, context []*Definition) error {
	var defs []*Definition
	if entry == "task" {
		def, err := ParseTaskDocument(path, []byte(body))
		if err != nil {
			return err
		}
		defs = []*Definition{def}
	} else {
		parsed, err := ParseDefinitionDocument(path, []byte(body))
		if err != nil {
			return err
		}
		defs = parsed
	}

	v := Validation{
		From:        Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath},
		Executables: NewExecutableRegistry(PluginExecutables{Alias: fixtureAlias, Path: fixturePath, Names: fixtureExecutables}),
	}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(append([]*Definition(nil), context...), defs...)}}, nil)

	sorted := append([]*Definition(nil), defs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, def := range sorted {
		if err := v.ValidateDefinition(def); err != nil {
			return err
		}
	}
	for _, def := range sorted {
		switch def.Kind {
		case KindWorkflow:
			if err := v.ValidatePlan(def, registry); err != nil {
				return err
			}
		case KindTask:
			if err := v.ValidateTaskContracts(def, registry); err != nil {
				return err
			}
		}
	}
	return nil
}

func fixtureContextDefs(t *testing.T, fixtureRoot string) []*Definition {
	t.Helper()
	var context []*Definition
	for _, rel := range fixtureContext {
		raw, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		defs, err := ParseDefinitionDocument(rel, []byte(fixtureBody(string(raw))))
		if err != nil {
			t.Fatalf("fixtureContext %s: %v", rel, err)
		}
		context = append(context, defs...)
	}
	return context
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
