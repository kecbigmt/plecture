package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// reviewFixtureWithOutput is a fixture task whose checks_status output is
// sourced by the given script. script must contain no double quotes.
func reviewFixtureWithOutput(script string) taskFixture {
	return taskFixture{id: "review", scope: "session", extra: "" +
		"[[outputs]]\nname = \"checks_status\"\nscript = \"" + script + "\"\n\n" +
		"[[done_when.all]]\ncheck = \"checks_status\"\neq = \"SUCCESS\"\n"}
}

func TestRefreshInstanceOutputs_PersistsFetchedValue(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{reviewFixtureWithOutput("echo SUCCESS")},
		[]nodeFixture{{id: "review"}})

	now := time.Now()
	store.Put(&domain.Session{
		Name:             "owner/repo-1",
		WorkspaceDirPath: t.TempDir(),
		Workflow:         "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": {TaskID: "review", Dynamic: true, Status: contract.TaskStatusProduced, Resource: "pr", Outputs: map[string]any{}},
		},
		CreatedAt: now, UpdatedAt: now,
	})

	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-1", "review#1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Fetched || results[0].Value != "SUCCESS" {
		t.Fatalf("results = %+v, want one fetched checks_status=SUCCESS", results)
	}
	if got := store.Get("owner/repo-1").Tasks["review#1"].Outputs["checks_status"]; got != "SUCCESS" {
		t.Errorf("persisted checks_status = %v, want SUCCESS", got)
	}
}

// A fetch failure is reported and leaves the prior value untouched (fail closed).
func TestRefreshInstanceOutputs_FetchFailureKeepsPrior(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{reviewFixtureWithOutput("exit 1")},
		[]nodeFixture{{id: "review"}})

	store.Put(&domain.Session{
		Name:             "owner/repo-2",
		WorkspaceDirPath: t.TempDir(),
		Workflow:         "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": {TaskID: "review", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{"checks_status": "PENDING"}},
		},
	})

	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-2", "review#1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Fetched || results[0].Error == "" {
		t.Fatalf("results = %+v, want one fetch failure with an error", results)
	}
	if got := store.Get("owner/repo-2").Tasks["review#1"].Outputs["checks_status"]; got != "PENDING" {
		t.Errorf("prior value must survive a fetch failure, got %v", got)
	}
}

func TestRefreshSessionOutputs_RefreshesDynamicInstancesOnly(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{reviewFixtureWithOutput("echo SUCCESS"), {id: "tmux", scope: "run"}},
		[]nodeFixture{{id: "review"}, {id: "tmux"}})
	store.Put(&domain.Session{
		Name: "owner/repo-9", WorkspaceDirPath: t.TempDir(), Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": {TaskID: "review", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
			"tmux":     {TaskID: "tmux", Status: contract.TaskStatusProduced},
		},
	})
	results, err := RefreshSessionOutputs(cfg, store, "owner/repo-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Instance != "review#1" {
		t.Fatalf("results = %+v, want only review#1 refreshed", results)
	}
	if got := store.Get("owner/repo-9").Tasks["review#1"].Outputs["checks_status"]; got != "SUCCESS" {
		t.Errorf("checks_status = %v, want SUCCESS", got)
	}
}

// One script (a produces group) yields several outputs in one fetch.
func TestRefreshInstanceOutputs_ProducesGroup(t *testing.T) {
	store := testStore(t)
	extra := "[[outputs]]\nproduces = [\"checks_status\", \"pr_state\"]\n" +
		"script = '''echo '{\"checks_status\":\"SUCCESS\",\"pr_state\":\"open\"}' '''\n"
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "review", scope: "session", extra: extra}}, []nodeFixture{{id: "review"}})
	store.Put(&domain.Session{
		Name: "owner/repo-8", WorkspaceDirPath: t.TempDir(), Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"review#1": {TaskID: "review", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		},
	})
	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-8", "review#1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want one per produced output", results)
	}
	got := store.Get("owner/repo-8").Tasks["review#1"].Outputs
	if got["checks_status"] != "SUCCESS" || got["pr_state"] != "open" {
		t.Errorf("outputs = %v, want both from one fetch", got)
	}
}

// A builtin output (no [[outputs]] declared) fetches from the real workspace
// directory — local DoD rides the same mechanism as a remote check.
func TestRefreshInstanceOutputs_BuiltinWorkspaceDirDirty(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "work", scope: "session", extra: "[[done_when.all]]\ncheck = \"workspace_dir_dirty\"\neq = \"0\"\n"}},
		[]nodeFixture{{id: "work"}})

	wt := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	store.Put(&domain.Session{
		Name: "owner/repo-7", WorkspaceDirPath: wt, Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"work#1": {TaskID: "work", Dynamic: true, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		},
	})

	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-7", "work#1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Fetched || results[0].Name != "workspace_dir_dirty" || results[0].Value != "1" {
		t.Fatalf("results = %+v, want workspace_dir_dirty=1 (one untracked file)", results)
	}
}

func TestRefreshInstanceOutputs_NoDynamicOutputs(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "tmux", scope: "run"}},
		[]nodeFixture{{id: "tmux"}})
	store.Put(&domain.Session{
		Name: "owner/repo-3", WorkspaceDirPath: t.TempDir(), Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"tmux": {TaskID: "tmux", Status: contract.TaskStatusProduced},
		},
	})
	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-3", "tmux")
	if err != nil || results != nil {
		t.Fatalf("no dynamic outputs → nil results, got results=%+v err=%v", results, err)
	}
}
