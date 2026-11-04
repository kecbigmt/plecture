package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestWorkflowRebaseRestartPreservesSessionNodesAndRetriesNewRevision(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "retry")
	cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{
		{id: "workspace", scope: "session", setup: "echo '{\"key\":\"kept\"}'", cleanup: "true"},
		{id: "runtime", scope: "run", setup: "echo '{\"version\":\"old\"}'", cleanup: "true"},
	}, []nodeFixture{{id: "workspace"}, {id: "runtime"}})
	addWorkflowFields(t, cfg, "work", "[work.health]\nauto_repair_attempts = 2\n")
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
	pinSessionRevision(t, cfg, session)
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	before := store.Get("one")
	path := filepath.Join(cfg.BaseDir, "tasks", "runtime.toml")
	newSetup := "if [ ! -e '" + marker + "' ]; then touch '" + marker + "'; exit 1; fi; echo '{\"version\":\"new\"}'"
	if err := os.WriteFile(path, []byte(effectFixtureDoc(taskFixture{id: "runtime", scope: "run", setup: newSetup, cleanup: "true"})), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", DryRun: true})
	if err != nil || preview.Class != RebaseRestart || !reflect.DeepEqual(preview.RecreateNodes, []string{"runtime"}) {
		t.Fatalf("preview = %+v, err = %v", preview, err)
	}
	if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one"}); err == nil || !strings.Contains(err.Error(), "--with-restart") {
		t.Fatalf("missing restart error = %v", err)
	}
	if store.Get("one").WorkflowRevision != before.WorkflowRevision {
		t.Fatal("rejected rebase changed revision")
	}
	if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", WithRestart: true}); err == nil {
		t.Fatal("first new setup should fail")
	}
	after := store.Get("one")
	if after.WorkflowRevision == before.WorkflowRevision || after.Status != contract.SessionStatusUp {
		t.Fatalf("failed rebase revision/status = %s/%s", after.WorkflowRevision, after.Status)
	}
	if after.Nodes["workspace"] == nil || after.Nodes["workspace"].ExecutionID != before.Nodes["workspace"].ExecutionID {
		t.Fatal("session node was recreated")
	}
	if attempted, err := RepairSession(cfg, store, "one", nil, NewRepairGate()); err != nil || !attempted {
		t.Fatalf("automatic repair = (%t, %v)", attempted, err)
	}
	if got := store.Get("one").Nodes["runtime"].Outputs["version"]; got != "new" {
		t.Fatalf("runtime version = %v", got)
	}
}

func TestWorkflowRebaseAutomaticallyAddsNode(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'"}}, []nodeFixture{{id: "runtime"}})
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
	pinSessionRevision(t, cfg, session)
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	before := store.Get("one")
	path := filepath.Join(cfg.BaseDir, "workflows", "work.toml")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n[[work.nodes]]\nid = \"extra\"\nuses = \"runtime\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", DryRun: true})
	if err != nil || preview.Class != RebaseAutomatic || !reflect.DeepEqual(preview.RecreateNodes, []string{"extra"}) {
		t.Fatalf("preview = %+v, err = %v", preview, err)
	}
	if store.Get("one").WorkflowRevision != before.WorkflowRevision {
		t.Fatal("dry run changed revision")
	}
	if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	after := store.Get("one")
	if after.WorkflowRevision == before.WorkflowRevision || after.Nodes["extra"] == nil || after.Nodes["extra"].Status != contract.TaskStatusProduced {
		t.Fatalf("automatic rebase did not produce new node: %+v", after)
	}
	if after.Nodes["runtime"].ExecutionID != before.Nodes["runtime"].ExecutionID {
		t.Fatal("unchanged node was recreated")
	}
	events, _, _, err := eventlog.NewStore(store.Dir()).List("one", 0, event.Filter{Types: []string{"plect.session.rebase"}})
	if err != nil || len(events) != 1 || events[0].Metadata["class"] != string(RebaseAutomatic) || events[0].Metadata["result"] != "succeeded" {
		t.Fatalf("automatic rebase events = %+v, err = %v", events, err)
	}
}

func TestWorkflowRebasePreservesStoppedAndSleepingSessions(t *testing.T) {
	for _, sleeping := range []bool{false, true} {
		name := "stopped"
		if sleeping {
			name = "sleeping"
		}
		t.Run(name, func(t *testing.T) {
			cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'", cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
			store := testStore(t)
			now := time.Now()
			session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
			pinSessionRevision(t, cfg, session)
			if err := store.Put(session); err != nil {
				t.Fatal(err)
			}
			if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
				t.Fatal(err)
			}
			if _, err := Down(cfg, store, DownParams{Identifier: "one", Sleep: sleeping}); err != nil {
				t.Fatal(err)
			}
			before := store.Get("one")
			addWorkflowFields(t, cfg, "work", "name = \"Updated\"\n")
			if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one"}); err != nil {
				t.Fatal(err)
			}
			after := store.Get("one")
			if after.WorkflowRevision == before.WorkflowRevision || after.Status != contract.SessionStatusDown || after.SleepSince.IsZero() != before.SleepSince.IsZero() || !reflect.DeepEqual(after.Nodes["runtime"], before.Nodes["runtime"]) {
				t.Fatalf("rebase changed %s session lifecycle: before status=%s sleep=%v node=%+v; after status=%s sleep=%v node=%+v", name, before.Status, before.SleepSince, before.Nodes["runtime"], after.Status, after.SleepSince, after.Nodes["runtime"])
			}
		})
	}
}

func TestWorkflowRebaseAliveProbeKeepsLiveSessionNode(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned")
	oldTask := taskFixture{id: "workspace", scope: "session", setup: "echo '{}'", cleanup: "touch '" + marker + "'", alive: "true"}
	cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{oldTask}, []nodeFixture{{id: "workspace"}})
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
	pinSessionRevision(t, cfg, session)
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	before := store.Get("one")
	oldTask.alive = "test 1 -eq 1"
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", "workspace.toml"), []byte(effectFixtureDoc(oldTask)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	after := store.Get("one")
	if after.Nodes["workspace"] == nil || after.Nodes["workspace"].ExecutionID != before.Nodes["workspace"].ExecutionID {
		t.Fatal("live session node was recreated")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("session cleanup ran: %v", err)
	}
}

func TestWorkflowRebaseRejectsSessionDeclarationChange(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{{id: "workspace", scope: "session", setup: "echo '{}'", cleanup: "true"}}, []nodeFixture{{id: "workspace"}})
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
	pinSessionRevision(t, cfg, session)
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.BaseDir, "tasks", "workspace.toml")
	if err := os.WriteFile(path, []byte(effectFixtureDoc(taskFixture{id: "workspace", scope: "session", setup: "printf '{}'", cleanup: "true"})), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", DryRun: true})
	if err != nil || preview.Class != RebaseRecreate || len(preview.RecreateNodes) != 0 {
		t.Fatalf("preview = %+v, err = %v", preview, err)
	}
	if _, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", WithRestart: true}); err == nil || !strings.Contains(err.Error(), "destroy and recreate") {
		t.Fatalf("rebase error = %v", err)
	}
	if store.Get("one").WorkflowRevision != session.WorkflowRevision {
		t.Fatal("rejected rebase changed revision")
	}
}

func TestWorkflowRebaseDryRunAndCompatibleApply(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'"}}, []nodeFixture{{id: "runtime"}})
	store := testStore(t)
	now := time.Now()
	session := &domain.Session{Name: "one", Workflow: "work", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}
	pinSessionRevision(t, cfg, session)
	if err := store.Put(session); err != nil {
		t.Fatal(err)
	}
	addWorkflowFields(t, cfg, "work", "name = \"Updated\"\n")
	preview, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Class != RebaseCompatible || store.Get("one").WorkflowRevision != session.WorkflowRevision {
		t.Fatalf("dry run = %+v, stored revision changed", preview)
	}
	result, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Class != RebaseCompatible || store.Get("one").WorkflowRevision == session.WorkflowRevision {
		t.Fatalf("apply = %+v, revision unchanged", result)
	}
	events, _, _, err := eventlog.NewStore(store.Dir()).List("one", 0, event.Filter{Types: []string{"plect.session.rebase"}})
	if err != nil || len(events) != 1 || events[0].Metadata["from"] != session.WorkflowRevision || events[0].Metadata["to"] != result.To || events[0].Metadata["class"] != string(RebaseCompatible) || events[0].Metadata["result"] != "succeeded" {
		t.Fatalf("rebase events = %+v, err = %v", events, err)
	}
}

func TestClassifyWorkflowRebase(t *testing.T) {
	base := func() config.WorkflowRevision {
		return config.WorkflowRevision{
			Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "workspace", Uses: "workspace"}, {ID: "runtime", Uses: "runtime"}}},
			Effects: map[string]config.TaskDefinition{
				"workspace": {ID: "workspace", Scope: "session", Setup: &lang.Action{Type: lang.ActionNoop}},
				"runtime":   {ID: "runtime", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}},
			},
		}
	}
	for _, tc := range []struct {
		name     string
		edit     func(*config.WorkflowRevision)
		class    RebaseClass
		recreate []string
	}{
		{"display", func(r *config.WorkflowRevision) { r.Workflow.Name = "changed" }, RebaseCompatible, nil},
		{"add node", func(r *config.WorkflowRevision) {
			r.Workflow.Nodes = append(r.Workflow.Nodes, config.WorkflowNode{ID: "extra", Uses: "runtime"})
		}, RebaseAutomatic, []string{"extra"}},
		{"alive probe", func(r *config.WorkflowRevision) {
			d := r.Effects["runtime"]
			d.Health = &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionNoop}}
			r.Effects["runtime"] = d
		}, RebaseAutomatic, nil},
		{"session alive probe", func(r *config.WorkflowRevision) {
			d := r.Effects["workspace"]
			d.Health = &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionNoop}}
			r.Effects["workspace"] = d
		}, RebaseAutomatic, nil},
		{"removed definition", func(r *config.WorkflowRevision) {
			r.Workflow.Nodes = r.Workflow.Nodes[:1]
			delete(r.Effects, "runtime")
		}, RebaseRecreate, nil},
		{"replace run nodes", func(r *config.WorkflowRevision) {
			r.Workflow.Nodes[1] = config.WorkflowNode{ID: "replacement", Uses: "runtime"}
		}, RebaseRestart, []string{"replacement"}},
		{"run declaration", func(r *config.WorkflowRevision) {
			d := r.Effects["runtime"]
			d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
			r.Effects["runtime"] = d
		}, RebaseRestart, []string{"runtime"}},
		{"restart lists all run nodes", func(r *config.WorkflowRevision) {
			r.Workflow.Nodes = append(r.Workflow.Nodes, config.WorkflowNode{ID: "extra", Uses: "runtime"})
			d := r.Effects["runtime"]
			d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
			r.Effects["runtime"] = d
		}, RebaseRestart, []string{"extra", "runtime"}},
		{"session declaration", func(r *config.WorkflowRevision) {
			d := r.Effects["workspace"]
			d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
			r.Effects["workspace"] = d
		}, RebaseRecreate, nil},
		{"unresolved input", func(r *config.WorkflowRevision) {
			r.Workflow.Nodes[1].Inputs = map[string]*lang.Value{"required": {Form: lang.FormFrom, From: "session.inputs.missing"}}
		}, RebaseRecreate, nil},
		{"unresolved channel input", func(r *config.WorkflowRevision) {
			r.Workflow.Event.Channel = []config.EventChannel{{Name: "notice", Inputs: map[string]*lang.Value{"required": {Form: lang.FormFrom, From: "nodes.runtime.outputs.missing"}}}}
		}, RebaseRecreate, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, next := base(), base()
			tc.edit(&next)
			got, err := ClassifyWorkflowRebase(old, next, &domain.Session{Inputs: map[string]any{}, Nodes: map[string]*contract.TaskState{}})
			if err != nil {
				t.Fatal(err)
			}
			if got.Class != tc.class || !reflect.DeepEqual(got.RecreateNodes, tc.recreate) {
				t.Fatalf("class = %s, recreate = %v; want %s, %v", got.Class, got.RecreateNodes, tc.class, tc.recreate)
			}
		})
	}
}
