package lang

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var docFixtureRe = regexp.MustCompile("(?m)^<!-- fixture: (\\S+) -->\\n```(?:toml|markdown)\\n((?s:.*?))```\\n")

// A chapter's worked example is the fixture, not a paraphrase of it: prose
// that drifts from the executable specification is worse than no prose. This
// is the one assertion the retired one-time specification tool made that
// nothing else covers — the registry against the diagnostics table is
// TestCodesMatchDocumentedTable's, and the fixtures themselves belong to the
// two conformance harnesses.
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
