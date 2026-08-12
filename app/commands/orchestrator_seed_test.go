package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The per-tick kick shrinks from pasting templates/orchestrator.md
// (~20KB) into the pane every tick to a short reminder that points at
// CLAUDE.md/AGENTS.md (seeded by the agent_docs task; wiring is covered by
// TestShippedConfig_OrchestratorWorkflowCompiles in internal/task). Like
// TestOrchestratorTemplate_RoutesActuationThroughTick, these config files live
// outside the plecture Nix package's fileset, so resolve from the repo root and
// skip when the checkout doesn't include them.
func TestOrchestratorTickTemplate_IsMinimal(t *testing.T) {
	repoRoot := orchestratorConfigRepoRoot(t)

	tickPath := filepath.Join(repoRoot, "config", "plecture", "templates", "orchestrator_tick.md")
	tick := readOrSkip(t, tickPath)
	fullPath := filepath.Join(repoRoot, "config", "plecture", "templates", "orchestrator.md")
	full := readOrSkip(t, fullPath)

	if len(tick) >= len(full) {
		t.Errorf("orchestrator_tick.md (%d bytes) should be far shorter than orchestrator.md (%d bytes)", len(tick), len(full))
	}
	if strings.Contains(tick, "## Rules") {
		t.Error("orchestrator_tick.md looks like it duplicates the full procedure rather than pointing at CLAUDE.md/AGENTS.md")
	}
}

func orchestratorConfigRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	// commands/ -> app/ -> plecture/ -> tools/ -> repo root
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
}

func readOrSkip(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("%s not present (outside this build's checkout scope)", path)
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
