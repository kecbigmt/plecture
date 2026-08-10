package dispatch

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

func writeEnvironmentFile(t *testing.T, baseDir, id, toml string) {
	t.Helper()
	writeFile(t, filepath.Join(baseDir, "environments", id+".toml"), toml)
}

func TestBuildChannelEnvironmentExecutor_NilForHostDegeneration(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	wf := config.WorkflowFile{ID: "wf"}
	s := &domain.Session{Tasks: map[string]*contract.TaskState{}}
	ex, err := buildChannelEnvironmentExecutor(cfg, wf, s)
	if err != nil {
		t.Fatal(err)
	}
	if ex != nil {
		t.Errorf("expected nil executor for a workflow with no environment, got %v", ex)
	}
}

// TestBuildChannelEnvironmentExecutor_NilWhenEnvironmentNotProduced is the
// direct regression test for the fail-closed gap a reviewer flagged: a
// workflow's environment that failed setup (or never ran it) must not hand
// back a live executor built from stale/missing outputs — deliverExec's own
// fail-closed check on a nil executor is what actually stops delivery, but it
// only works if this function returns nil here in the first place.
func TestBuildChannelEnvironmentExecutor_NilWhenEnvironmentNotProduced(t *testing.T) {
	baseDir := t.TempDir()
	writeEnvironmentFile(t, baseDir, "docker", `exec = "docker exec -i x \"$@\""`)
	cfg := &config.Config{BaseDir: baseDir}
	wf := config.WorkflowFile{ID: "wf", Environment: "docker"}

	cases := []struct {
		name  string
		tasks map[string]*contract.TaskState
	}{
		{"missing @environment key", map[string]*contract.TaskState{}},
		{"@environment failed", map[string]*contract.TaskState{
			contract.EnvironmentPseudoNodeID: {Status: contract.TaskStatusFailed, Outputs: map[string]any{"workdir": "/stale"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &domain.Session{Tasks: tc.tasks}
			ex, err := buildChannelEnvironmentExecutor(cfg, wf, s)
			if err != nil {
				t.Fatal(err)
			}
			if ex != nil {
				t.Errorf("expected nil executor when @environment is not produced, got %v", ex)
			}
		})
	}
}

func TestBuildChannelEnvironmentExecutor_NonNilWhenProduced(t *testing.T) {
	baseDir := t.TempDir()
	writeEnvironmentFile(t, baseDir, "docker", `exec = "docker exec -i x \"$@\""`)
	cfg := &config.Config{BaseDir: baseDir}
	wf := config.WorkflowFile{ID: "wf", Environment: "docker"}
	s := &domain.Session{Tasks: map[string]*contract.TaskState{
		contract.EnvironmentPseudoNodeID: {Status: contract.TaskStatusProduced, Outputs: map[string]any{"workdir": "/env/wd"}},
	}}
	ex, err := buildChannelEnvironmentExecutor(cfg, wf, s)
	if err != nil {
		t.Fatal(err)
	}
	if ex == nil {
		t.Fatal("expected a non-nil executor when @environment is produced")
	}
}
