package lang

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCodesMatchDocumentedTable guards against the registry drifting from
// docs/language/README.md's diagnostics table: a code added, removed, or
// reassigned to a different layer in the docs without a matching edit here
// would otherwise go unnoticed until a much later slice tried to use it.
func TestCodesMatchDocumentedTable(t *testing.T) {
	documented := readDocumentedDiagnostics(t)

	got := map[Code][]Layer{}
	for _, c := range Codes() {
		got[c] = codeLayers[c]
	}
	if len(got) != len(documented) {
		t.Fatalf("registry has %d codes, docs table has %d", len(got), len(documented))
	}
	for code, layers := range documented {
		gotLayers, ok := got[Code(code)]
		if !ok {
			t.Errorf("docs table documents %s, but it is not in Codes()", code)
			continue
		}
		gotSet := map[string]bool{}
		for _, l := range gotLayers {
			gotSet[string(l)] = true
		}
		for _, l := range layers {
			if !gotSet[l] {
				t.Errorf("%s: docs table documents layer %q, registry does not allow it", code, l)
			}
		}
		if len(gotSet) != len(layers) {
			t.Errorf("%s: registry allows %v, docs table documents %v", code, gotLayers, layers)
		}
	}
}

func TestValidLayerRejectsUndocumentedCombination(t *testing.T) {
	if ValidLayer(CodeKindMissing, LayerSemantic) {
		t.Fatal("PLECTURE-CFG-KIND-MISSING is documented as structural only")
	}
	if !ValidLayer(CodeFromRoot, LayerStructural) || !ValidLayer(CodeFromRoot, LayerSemantic) {
		t.Fatal("PLECTURE-CFG-FROM-ROOT is documented as both structural and semantic")
	}
}

func TestNewDiagPanicsOnUndocumentedLayer(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an undocumented code/layer pair")
		}
	}()
	newDiag(CodeKindMissing, LayerSemantic, Position{}, "should never construct")
}

var readmeRowRe = regexp.MustCompile("^\\| `(PLECTURE-CFG-[A-Z0-9-]+)` \\| ([a-z / ]+) \\|")

// readDocumentedDiagnostics parses the diagnostics table out of
// docs/language/README.md, the same source of truth the (now-retired)
// the conformance harness reads.
func readDocumentedDiagnostics(t *testing.T) map[string][]string {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(root + "/docs/language/README.md")
	if err != nil {
		t.Fatalf("read docs/language/README.md: %v", err)
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		m := readmeRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		layers := strings.Split(m[2], "/")
		for i, l := range layers {
			layers[i] = strings.TrimSpace(l)
		}
		sort.Strings(layers)
		out[m[1]] = layers
	}
	if len(out) == 0 {
		t.Fatal("no diagnostic rows parsed from docs/language/README.md")
	}
	return out
}
