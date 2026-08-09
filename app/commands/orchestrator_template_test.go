package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// story PR-C #6 (driving-path parity): the orchestrator template must route
// its actuation-dependent instructions (kick emission, review/escalate
// tracking) through `tws tick`, not the now observation-only `tws check`.
// The template lives outside the tws Nix package's fileset (tools/tws only),
// so this test resolves it relative to the repo root and skips when absent
// (e.g. a sandboxed build that only checks out tools/tws).
func TestOrchestratorTemplate_RoutesActuationThroughTick(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	// commands/ -> app/ -> tws/ -> tools/ -> repo root
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	path := filepath.Join(repoRoot, "config", "tws", "templates", "orchestrator.md")

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("orchestrator.md not present at %s (outside this build's checkout scope)", path)
		}
		t.Fatalf("read orchestrator.md: %v", err)
	}
	body := string(b)

	if !strings.Contains(body, "tws tick") {
		t.Error("orchestrator.md does not mention `tws tick`; the actuation-dependent instructions must route through it")
	}
	for _, stale := range []string{
		"tws check <session> --json` and its `review_required`",
		"then run `tws check <session>` so the same",
		"run `tws check <session>` and proceed only when",
		"passed through\n  `tws check <session>",
	} {
		if strings.Contains(body, stale) {
			t.Errorf("orchestrator.md still contains a pre-split actuation instruction: %q", stale)
		}
	}
}
