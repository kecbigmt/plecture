package legacyimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/persistence"
)

// withChildTasks rewrites the fixture's state.json so child-session carries
// tasks, as a legacy store written after nested layers existed would.
func withChildTasks(t *testing.T, sourceDir string, tasks map[string]any) {
	t.Helper()
	path := filepath.Join(sourceDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state["sessions"].(map[string]any)["child-session"].(map[string]any)["tasks"] = tasks
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRun_LayerWithoutSetupTimeTakesItsInstancesSetupTime(t *testing.T) {
	sourceDir, _, _ := legacyFixture(t)
	withChildTasks(t, sourceDir, map[string]any{
		"agent": map[string]any{
			"task_id": "agent", "scope": "session", "status": "produced", "setup_at": "2026-01-02T03:04:05Z",
			"layers": []any{
				map[string]any{"effect_id": "terminal", "status": "produced"},
				map[string]any{"effect_id": "agent", "status": "produced", "setup_at": "2026-01-02T03:04:06Z"},
			},
		},
		"review#1": map[string]any{
			"task_id": "review", "scope": "run", "status": "produced", "setup_at": "2026-01-02T03:04:07Z", "dynamic": true,
			"layers": []any{map[string]any{"effect_id": "checkout", "status": "produced"}},
		},
	})
	ctx := context.Background()

	report, err := Run(ctx, Options{SourceDir: sourceDir, DestDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	db, err := persistence.EnsureCurrent(ctx, report.DBPath)
	if err != nil {
		t.Fatalf("open promoted database: %v", err)
	}
	defer db.Close()
	child, err := db.GetSession(ctx, "child-session")
	if err != nil || child == nil {
		t.Fatalf("GetSession(child-session) = %v, %v", child, err)
	}
	if child.Nodes["agent"] == nil || child.Tasks["review#1"] == nil {
		t.Fatalf("child nodes %v, tasks %v, want node agent and task review#1", child.Nodes, child.Tasks)
	}
	got := []string{}
	for _, layer := range append(child.Nodes["agent"].Layers, child.Tasks["review#1"].Layers...) {
		got = append(got, layer.EffectID+"@"+layer.SetupAt.UTC().Format(time.RFC3339))
	}
	want := []string{"terminal@2026-01-02T03:04:05Z", "agent@2026-01-02T03:04:06Z", "checkout@2026-01-02T03:04:07Z"}
	if !slices.Equal(got, want) {
		t.Errorf("layer setup times = %q, want %q", got, want)
	}
}

func TestRun_RefusesALayerWhoseInstanceHasNoSetupTimeEither(t *testing.T) {
	sourceDir, _, _ := legacyFixture(t)
	withChildTasks(t, sourceDir, map[string]any{
		"initial": map[string]any{
			"task_id": "work", "scope": "session", "status": "failed",
			"layers": []any{map[string]any{"effect_id": "checkout", "status": "failed"}},
		},
	})
	destDir := t.TempDir()

	_, err := Run(context.Background(), Options{SourceDir: sourceDir, DestDir: destDir})
	if err == nil {
		t.Fatal("Run succeeded, want a refusal for a layer with no setup time to copy")
	}
	for _, want := range []string{"child-session", "initial", "checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
	if _, statErr := os.Stat(persistence.PathIn(destDir)); !os.IsNotExist(statErr) {
		t.Errorf("a refused import left a database behind: %v", statErr)
	}
}
