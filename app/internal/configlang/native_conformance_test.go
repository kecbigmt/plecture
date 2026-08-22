package configlang

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

// nativeDeferred names every fixture whose documented diagnostic a later
// implementation slice produces, with the check that is still missing. A
// fixture leaves this map when that check lands; nothing else may be
// skipped.
var nativeDeferred = map[string]string{
	"effects/chains-field.invalid.toml":            "per-kind closed-surface validation",
	"effects/completion-field.invalid.toml":        "per-kind closed-surface validation",
	"tasks/lifecycle-field.invalid.md":             "per-kind closed-surface validation",
	"nesting/computed-output-mutable.invalid.toml": "the nesting joint's mutability agreement",
	"nesting/cycle.invalid.toml":                   "nesting chain resolution",
	"nesting/projection-mismatch.invalid.toml":     "the nesting joint's type agreement",
	"workflows/node-cycle.invalid.toml":            "the workflow dependency graph",
	"tasks/unpublished-key.invalid.md":             "contract-key resolution against the declared observer",
	"tasks/first-observe-failed.invalid.md":        "instantiation",
	"tasks/resource-mismatch.invalid.md":           "instantiation",
	"references/alias-required.invalid.toml":       "reference resolution across enabled plugin layers",
	"references/cross-plugin.invalid.toml":         "reference resolution across enabled plugin layers",
	"references/duplicate-id.invalid.toml":         "duplicate-id detection across a layer's files",
	"references/unknown-ref.invalid.toml":          "reference resolution across enabled plugin layers",
	"references/wrong-kind.invalid.toml":           "reference resolution across enabled plugin layers",
	"references/task-in-node.invalid.toml":         "reference resolution across enabled plugin layers",
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

			got := nativeLoad(rel, exp.Entry, fixtureBody(string(raw)))
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

// nativeLoad runs one fixture through the load pipeline this package
// implements: parse, then value, expression, action, and executable
// validation, then the plan-level capability check.
func nativeLoad(path, entry, body string) error {
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
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: defs}}, nil)

	sorted := append([]*Definition(nil), defs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, def := range sorted {
		if err := v.ValidateDefinition(def); err != nil {
			return err
		}
	}
	for _, def := range sorted {
		if def.Kind != KindWorkflow {
			continue
		}
		if err := v.ValidatePlan(def, registry); err != nil {
			return err
		}
	}
	return nil
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
