package lang

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The two checks here are the ones the conformance harness could not absorb
// when it took over from the specification PR's one-time tool: that harness
// reads the diagnostic registry, so it cannot notice the registry drifting
// from the table that documents it, and it reads fixtures, so it cannot
// notice a chapter quoting one inexactly. Both guard rules stated in
// docs/language/README.md and CLAUDE.md, both can be broken by an edit that
// looks correct in isolation, and neither costs anything per legitimate
// change — the assertion is an equality between two files, never a pinned
// literal.

var (
	docDiagnosticRe = regexp.MustCompile(`PLECTURE-CFG-[A-Z0-9-]+`)
	docFixtureRe    = regexp.MustCompile("(?m)^<!-- fixture: (\\S+) -->\\n```(?:toml|markdown)\\n((?s:.*?))```\\n")
)

// documentedDiagnostics reads the diagnostic table in docs/language/README.md.
func documentedDiagnostics(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "language", "README.md"))
	if err != nil {
		t.Fatalf("read the diagnostics chapter: %v", err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		for _, code := range docDiagnosticRe.FindAllString(line, -1) {
			out[code] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no diagnostic codes found in the diagnostics chapter")
	}
	return out
}

// A diagnostic the language can raise is one an author can meet, so the table
// that documents them and the registry that defines them name the same set.
func TestDocumentedDiagnosticsMatchTheRegistry(t *testing.T) {
	documented := documentedDiagnostics(t)
	registered := map[string]bool{}
	for _, c := range Codes() {
		registered[string(c)] = true
	}
	for code := range registered {
		if !documented[code] {
			t.Errorf("%s is registered but the diagnostics chapter does not list it", code)
		}
	}
	for code := range documented {
		if !registered[code] {
			t.Errorf("%s is listed in the diagnostics chapter but no longer registered", code)
		}
	}
}

// A chapter's worked example is the fixture, not a paraphrase of it: prose
// that drifts from the executable specification is worse than no prose.
func TestChapterExamplesQuoteTheirFixtureVerbatim(t *testing.T) {
	root := repoRoot(t)
	fixtureRoot := filepath.Join(root, "testdata", "config-language")
	chapters, err := filepath.Glob(filepath.Join(root, "docs", "language", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(chapters)

	quoted := 0
	for _, chapter := range chapters {
		raw, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range docFixtureRe.FindAllStringSubmatch(string(raw), -1) {
			name, block := m[1], m[2]
			fixture, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(name)))
			if err != nil {
				t.Errorf("%s names fixture %s, which does not exist", filepath.Base(chapter), name)
				continue
			}
			quoted++
			if strings.TrimRight(block, "\n") != strings.TrimRight(fixtureBody(string(fixture)), "\n") {
				t.Errorf("%s: the example for %s is not the fixture verbatim", filepath.Base(chapter), name)
			}
		}
	}
	if quoted == 0 {
		t.Error("no chapter quotes a fixture; the worked examples have stopped being executable")
	}
}
