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

func TestClassifyWorkflowRebaseSubscriptionOnlyChange(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  RebaseClass
	}{
		{"session", RebaseRecreate},
		{"run", RebaseRestart},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			def := config.TaskDefinition{ID: "thread", Scope: tc.scope, Setup: &lang.Action{Type: lang.ActionNoop}}
			old := config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "thread", Uses: "thread"}}}, Effects: map[string]config.TaskDefinition{"thread": def}}
			next := old
			next.Workflow.Nodes = []config.WorkflowNode{{ID: "thread", Uses: "thread", Subscription: "permalink"}}
			got, err := ClassifyWorkflowRebase(old, next, &domain.Session{Nodes: map[string]*contract.TaskState{"thread": {Scope: tc.scope, Status: contract.TaskStatusProduced}}})
			if err != nil || got.Class != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestWorkflowRebaseRestartPreservesSessionNodesAndRetriesNewRevision(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "retry")
	cfg := writeWorkflowFixture(t, "work", []taskFixture{
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
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'"}}, []nodeFixture{{id: "runtime"}})
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
			cfg := writeWorkflowFixture(t, "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'", cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
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
	cfg := writeWorkflowFixture(t, "work", []taskFixture{oldTask}, []nodeFixture{{id: "workspace"}})
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

func TestWorkflowRebaseRestartCleansRemovedSessionNodeWithOldDefinition(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleanup")
	cfg := writeWorkflowFixture(t, "work", []taskFixture{
		{id: "workspace", scope: "session", setup: "echo '{}'", cleanup: "echo old > '" + marker + "'"},
		{id: "runtime", scope: "run", setup: "echo '{}'", cleanup: "true"},
	}, []nodeFixture{{id: "workspace"}, {id: "runtime"}})
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
	if _, err := Down(cfg, store, DownParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.BaseDir, "tasks", "workspace.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "workflows", "work.toml"), []byte("[work]\nkind = \"workflow\"\nresource = \"issue\"\n[[work.nodes]]\nuses = \"runtime\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", "runtime.toml"), []byte(effectFixtureDoc(taskFixture{id: "runtime", scope: "run", setup: "echo '{\"new\":true}'", cleanup: "true"})), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := WorkflowRebase(cfg, store, RebaseParams{Identifier: "one", WithRestart: true})
	if err != nil || result.Class != RebaseRestart {
		t.Fatalf("rebase = %+v, %v", result, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "old\n" {
		t.Fatalf("old cleanup = %q, %v", data, err)
	}
	after := store.Get("one")
	if after.Nodes["workspace"] != nil || after.Status != contract.SessionStatusDown {
		t.Fatalf("removed node/status = %+v/%s", after.Nodes["workspace"], after.Status)
	}
}

func TestClassifyWorkflowRebaseDefersBindingsFromNewAndFailedNodes(t *testing.T) {
	def := config.TaskDefinition{ID: "runtime", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}
	base := config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}}}, Effects: map[string]config.TaskDefinition{"runtime": def}}
	for _, tc := range []struct {
		name string
		old  config.WorkflowRevision
		next config.WorkflowRevision
		s    *domain.Session
		want RebaseClass
	}{
		{"added dependent pair", base, config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "a", Uses: "runtime"}, {ID: "b", Uses: "runtime", Inputs: map[string]*lang.Value{"x": {Form: lang.FormFrom, From: "nodes.a.outputs.x"}}}}}, Effects: base.Effects}, &domain.Session{Nodes: map[string]*contract.TaskState{}}, RebaseAutomatic},
		{"failed source and display edit", config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "consumer", Uses: "runtime", Inputs: map[string]*lang.Value{"x": {Form: lang.FormFrom, From: "nodes.runtime.outputs.x"}}}}}, Effects: base.Effects}, config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Name: "Updated", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "consumer", Uses: "runtime", Inputs: map[string]*lang.Value{"x": {Form: lang.FormFrom, From: "nodes.runtime.outputs.x"}}}}}, Effects: base.Effects}, &domain.Session{Nodes: map[string]*contract.TaskState{"runtime": {Status: contract.TaskStatusFailed, Scope: "run"}}}, RebaseCompatible},
		{"restart produces new input", config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "consumer", Uses: "runtime"}}}, Effects: base.Effects}, config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "consumer", Uses: "runtime", Inputs: map[string]*lang.Value{"x": {Form: lang.FormFrom, From: "nodes.runtime.outputs.new"}}}}}, Effects: map[string]config.TaskDefinition{"runtime": {ID: "runtime", Scope: "run", Setup: &lang.Action{Type: lang.ActionShell, Script: "true"}}}}, &domain.Session{Nodes: map[string]*contract.TaskState{"runtime": {Status: contract.TaskStatusProduced, Scope: "run", Outputs: map[string]any{}}}}, RebaseRestart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyWorkflowRebase(tc.old, tc.next, tc.s)
			if err != nil || got.Class != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
	t.Run("stable binding inside deferred JSON", func(t *testing.T) {
		next := config.WorkflowRevision{Workflow: config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "runtime", Uses: "runtime"}, {ID: "a", Uses: "runtime"}, {ID: "b", Uses: "runtime", Inputs: map[string]*lang.Value{"payload": {Form: lang.FormJSON, JSON: &lang.JSONOperand{Object: map[string]*lang.JSONOperand{
			"new":   {Leaf: &lang.Value{Form: lang.FormFrom, From: "nodes.a.outputs.x"}},
			"saved": {Leaf: &lang.Value{Form: lang.FormFrom, From: "session.inputs.missing"}},
		}}}}}}}, Effects: base.Effects}
		got, err := ClassifyWorkflowRebase(base, next, &domain.Session{Nodes: map[string]*contract.TaskState{}})
		if err != nil || got.Class != RebaseRecreate {
			t.Fatalf("classification = %+v, %v; want recreate", got, err)
		}
	})
}

func TestClassifyWorkflowRebaseDynamicEffects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope string
		edit  func(map[string]config.TaskDefinition)
		want  RebaseClass
	}{
		{"missing", "run", func(defs map[string]config.TaskDefinition) { delete(defs, "dynamic") }, RebaseRecreate},
		{"alive only", "session", func(defs map[string]config.TaskDefinition) {
			d := defs["dynamic"]
			d.Health = &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionNoop}}
			defs["dynamic"] = d
		}, RebaseAutomatic},
		{"run declaration", "run", func(defs map[string]config.TaskDefinition) {
			d := defs["dynamic"]
			d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
			defs["dynamic"] = d
		}, RebaseRestart},
		{"session declaration", "session", func(defs map[string]config.TaskDefinition) {
			d := defs["dynamic"]
			d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
			defs["dynamic"] = d
		}, RebaseRecreate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDef := config.TaskDefinition{ID: "dynamic", Scope: tc.scope, Setup: &lang.Action{Type: lang.ActionNoop}}
			wf := config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "static", Uses: "static"}}}
			staticDef := config.TaskDefinition{ID: "static", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}
			old := config.WorkflowRevision{Workflow: wf, Effects: map[string]config.TaskDefinition{"static": staticDef, "dynamic": oldDef}}
			next := config.WorkflowRevision{Workflow: wf, Effects: map[string]config.TaskDefinition{"static": staticDef, "dynamic": oldDef}}
			tc.edit(next.Effects)
			session := &domain.Session{Tasks: map[string]*contract.TaskState{"instance": {TaskID: "dynamic", Scope: tc.scope, Status: contract.TaskStatusProduced}}}
			got, err := ClassifyWorkflowRebase(old, next, session)
			if err != nil || got.Class != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*config.TaskDocument)
		want RebaseClass
	}{
		{"instruction", func(d *config.TaskDocument) { d.Instruction = "new" }, RebaseCompatible},
		{"description", func(d *config.TaskDocument) { d.Description = "new" }, RebaseCompatible},
		{"done_when", func(d *config.TaskDocument) {
			d.DoneWhen = &config.DoneWhen{All: []config.DoneWhenLeaf{{Judge: "approve", ID: "approval"}}}
		}, RebaseRecreate},
		{"inputs_schema", func(d *config.TaskDocument) { d.InputsSchema = map[string]any{"type": "object"} }, RebaseRecreate},
		{"chains", func(d *config.TaskDocument) { d.Chains = []config.DocumentChain{{ID: "review", Workflow: "review"}} }, RebaseRecreate},
	} {
		t.Run("task document "+tc.name, func(t *testing.T) {
			wf := config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "static", Uses: "static"}}}
			defs := map[string]config.TaskDefinition{"static": {ID: "static", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}}
			oldDoc := config.TaskDocument{ID: "review", Instruction: "old"}
			nextDoc := oldDoc
			tc.edit(&nextDoc)
			old := config.WorkflowRevision{Workflow: wf, Effects: defs, Tasks: map[string]config.TaskDocument{"review": oldDoc}}
			next := config.WorkflowRevision{Workflow: wf, Effects: defs, Tasks: map[string]config.TaskDocument{"review": nextDoc}}
			session := &domain.Session{Tasks: map[string]*contract.TaskState{"review#1": {TaskID: "review", Scope: "session", Status: contract.TaskStatusProduced}}}
			got, err := ClassifyWorkflowRebase(old, next, session)
			if err != nil || got.Class != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
	t.Run("task document inherited instruction", func(t *testing.T) {
		wf := config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "static", Uses: "static"}}}
		defs := map[string]config.TaskDefinition{"static": {ID: "static", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}}
		oldDoc := config.TaskDocument{ID: "review", Instruction: "old", ExtendsLayers: []config.TaskDocument{{ID: "base", Instruction: "old"}}}
		nextDoc := config.TaskDocument{ID: "review", Instruction: "new", ExtendsLayers: []config.TaskDocument{{ID: "base", Instruction: "new"}}}
		old := config.WorkflowRevision{Workflow: wf, Effects: defs, Tasks: map[string]config.TaskDocument{"review": oldDoc}}
		next := config.WorkflowRevision{Workflow: wf, Effects: defs, Tasks: map[string]config.TaskDocument{"review": nextDoc}}
		session := &domain.Session{Tasks: map[string]*contract.TaskState{"review#1": {TaskID: "review", Scope: "session", Status: contract.TaskStatusProduced}}}
		got, err := ClassifyWorkflowRebase(old, next, session)
		if err != nil || got.Class != RebaseCompatible {
			t.Fatalf("classification = %+v, %v; want compatible", got, err)
		}
	})
}

func TestClassifyWorkflowRebaseDynamicInstanceStatus(t *testing.T) {
	removed := func(defs map[string]config.TaskDefinition) { delete(defs, "dynamic") }
	changed := func(defs map[string]config.TaskDefinition) {
		d := defs["dynamic"]
		d.Setup = &lang.Action{Type: lang.ActionShell, Script: "true"}
		defs["dynamic"] = d
	}
	for _, tc := range []struct {
		name   string
		status string
		edit   func(map[string]config.TaskDefinition)
		want   RebaseClass
	}{
		{"failed removed", contract.TaskStatusFailed, removed, RebaseRecreate},
		{"produced removed", contract.TaskStatusProduced, removed, RebaseRecreate},
		{"cleaned removed", contract.TaskStatusCleaned, removed, RebaseCompatible},
		{"failed changed", contract.TaskStatusFailed, changed, RebaseRestart},
		{"produced changed", contract.TaskStatusProduced, changed, RebaseRestart},
		{"cleaned changed", contract.TaskStatusCleaned, changed, RebaseCompatible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDef := config.TaskDefinition{ID: "dynamic", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}
			staticDef := config.TaskDefinition{ID: "static", Scope: "run", Setup: &lang.Action{Type: lang.ActionNoop}}
			wf := config.WorkflowFile{Address: "work", Nodes: []config.WorkflowNode{{ID: "static", Uses: "static"}}}
			old := config.WorkflowRevision{Workflow: wf, Effects: map[string]config.TaskDefinition{"static": staticDef, "dynamic": oldDef}}
			next := config.WorkflowRevision{Workflow: wf, Effects: map[string]config.TaskDefinition{"static": staticDef, "dynamic": oldDef}}
			tc.edit(next.Effects)
			session := &domain.Session{Tasks: map[string]*contract.TaskState{"instance": {TaskID: "dynamic", Scope: "run", Status: tc.status}}}
			got, err := ClassifyWorkflowRebase(old, next, session)
			if err != nil || got.Class != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestWorkflowRebaseRejectsSessionDeclarationChange(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{id: "workspace", scope: "session", setup: "echo '{}'", cleanup: "true"}}, []nodeFixture{{id: "workspace"}})
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
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{id: "runtime", scope: "run", setup: "echo '{}'"}}, []nodeFixture{{id: "runtime"}})
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
		}, RebaseAutomatic, nil},
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
			got, err := ClassifyWorkflowRebase(old, next, &domain.Session{Inputs: map[string]any{}, Nodes: map[string]*contract.TaskState{"runtime": {Status: contract.TaskStatusProduced, Scope: "run"}}})
			if err != nil {
				t.Fatal(err)
			}
			if got.Class != tc.class || !reflect.DeepEqual(got.RecreateNodes, tc.recreate) {
				t.Fatalf("class = %s, recreate = %v; want %s, %v", got.Class, got.RecreateNodes, tc.class, tc.recreate)
			}
		})
	}
}

func TestClassifyWorkflowRebaseConsumerOfAChangedPublicOutput(t *testing.T) {
	from := func(path string) *lang.Value { return &lang.Value{Form: lang.FormFrom, From: path} }
	base := func(scope string) config.WorkflowRevision {
		return config.WorkflowRevision{
			Workflow: config.WorkflowFile{
				Address: "work",
				Outputs: map[string]*lang.Value{"endpoint": from("nodes.agent.outputs.endpoint")},
				Nodes: []config.WorkflowNode{
					{ID: "agent", Uses: "agent"},
					{ID: "other", Uses: "agent"},
					{ID: "relay", Uses: "relay", Inputs: map[string]*lang.Value{"peer": from("workflow.outputs.endpoint")}},
					{ID: "bystander", Uses: "agent"},
				},
			},
			Effects: map[string]config.TaskDefinition{
				"agent": {ID: "agent", Scope: scope, Setup: &lang.Action{Type: lang.ActionNoop}},
				"relay": {ID: "relay", Scope: scope, Setup: &lang.Action{Type: lang.ActionNoop}},
			},
		}
	}
	sessionFor := func(scope string) *domain.Session {
		return &domain.Session{Inputs: map[string]any{}, Nodes: map[string]*contract.TaskState{
			"agent":     {Status: contract.TaskStatusProduced, Scope: scope, Outputs: map[string]any{"endpoint": "e1"}},
			"other":     {Status: contract.TaskStatusProduced, Scope: scope, Outputs: map[string]any{"endpoint": "e2"}},
			"relay":     {Status: contract.TaskStatusProduced, Scope: scope},
			"bystander": {Status: contract.TaskStatusProduced, Scope: scope},
		}}
	}
	for _, tc := range []struct {
		name          string
		consumerScope string
		edit          func(*config.WorkflowRevision)
		class         RebaseClass
		recreate      []string
	}{
		{"binding unchanged", "run", func(*config.WorkflowRevision) {}, RebaseCompatible, nil},
		{"binding repointed", "run", func(r *config.WorkflowRevision) {
			r.Workflow.Outputs["endpoint"] = from("nodes.other.outputs.endpoint")
		}, RebaseRestart, []string{"agent", "bystander", "other", "relay"}},
		{"binding removed", "run", func(r *config.WorkflowRevision) {
			delete(r.Workflow.Outputs, "endpoint")
		}, RebaseRecreate, nil},
		{"unrelated binding changed", "run", func(r *config.WorkflowRevision) {
			r.Workflow.Outputs["unread"] = from("nodes.other.outputs.endpoint")
		}, RebaseCompatible, nil},
		{"session-scoped consumer", "session", func(r *config.WorkflowRevision) {
			r.Workflow.Outputs["endpoint"] = from("nodes.other.outputs.endpoint")
		}, RebaseRecreate, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, next := base(tc.consumerScope), base(tc.consumerScope)
			tc.edit(&next)
			got, err := ClassifyWorkflowRebase(old, next, sessionFor(tc.consumerScope))
			if err != nil {
				t.Fatal(err)
			}
			if got.Class != tc.class || !reflect.DeepEqual(got.RecreateNodes, tc.recreate) {
				t.Fatalf("class = %s, recreate = %v (%v); want %s, %v", got.Class, got.RecreateNodes, got.Reasons, tc.class, tc.recreate)
			}
		})
	}
}
