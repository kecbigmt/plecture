package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func subscriptionFixture(t *testing.T, limit string) *config.Config {
	t.Helper()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[item]
kind = "resource"
match = '^item://(?P<name>[a-z]+)(?:\?.*)?$'
name = { from = "match.name" }
` + limit + `
[item.observe]
type = "exec"
command = "printf"
args = ['{}']
`
	if err := os.WriteFile(filepath.Join(base, "resources", "item.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{BaseDir: base}
}

func TestRuntimeSubscriptionAndResourcePublishDeliverOnceAcrossSources(t *testing.T) {
	cfg := subscriptionFixture(t, "")
	store := state.NewStore(t.TempDir())
	for _, name := range []string{"first", "second"} {
		if err := store.Put(&domain.Session{Name: name, Resource: &contract.Resource{Type: "item", Name: "one", Locator: "item://one"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "first", ResourceID: "item://one"}); err != nil {
		t.Fatal(err)
	}
	count, err := EventPublishResource(cfg, store, "item://one?view=alternate", EventPublishParams{Type: event.TypeUserNote, Summary: "hello"})
	if err != nil || count != 2 {
		t.Fatalf("delivered = %d, err = %v", count, err)
	}
	for _, name := range []string{"first", "second"} {
		events, _, _, err := EventList(cfg, store, name, 0, event.Filter{})
		if err != nil || len(events) != 1 {
			t.Fatalf("%s events = %+v, err = %v", name, events, err)
		}
	}
	if err := Unsubscribe(cfg, store, SubscribeParams{SessionName: "first", ResourceID: "item://one?view=alternate"}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListSubscriptions("first", "")
	if err != nil || len(rows) != 1 || rows[0].Source != "dispatch" {
		t.Fatalf("rows after unsubscribe = %+v, err = %v", rows, err)
	}
}

func TestRuntimeSubscriptionRespectsSubscriberLimit(t *testing.T) {
	cfg := subscriptionFixture(t, "max_subscribers = 1")
	store := state.NewStore(t.TempDir())
	for _, name := range []string{"first", "second"} {
		if err := store.Put(&domain.Session{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "first", ResourceID: "item://one"}); err != nil {
		t.Fatal(err)
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "first", ResourceID: "item://one"}); err != nil {
		t.Fatalf("repeat subscription should be idempotent: %v", err)
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "second", ResourceID: "item://one"}); err == nil {
		t.Fatal("second subscriber should be rejected")
	}
}

func TestTaskSubscriptionEndsOnlyAfterLastBoundInstanceIsCleaned(t *testing.T) {
	cfg := writeWorkflowFixture(t, "coding",
		[]taskFixture{{id: "work", scope: "session", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "work"}},
	)
	store := testStore(t)
	seedSession(t, store, "case-1", "case", 1, "coding", map[string]*contract.TaskState{})
	for _, name := range []string{"first", "second"} {
		if _, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "work", SessionName: "case-1", Name: name, Resource: "item://one"}); err != nil {
			t.Fatalf("setup %s: %v", name, err)
		}
	}
	assertTaskRows := func(want int) {
		t.Helper()
		rows, err := store.ListSubscriptions("case-1", "")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range rows {
			if row.Source == "task" && row.Resource.Locator == "item://one" {
				count++
			}
		}
		if count != want {
			t.Fatalf("task rows = %d, want %d", count, want)
		}
	}
	assertTaskRows(1)
	first, err := TaskCleanup(cfg, store, TaskCleanupParams{SessionName: "case-1", Instance: "first"})
	if err != nil || first.Unsubscribed {
		t.Fatalf("first cleanup = %+v, err = %v", first, err)
	}
	assertTaskRows(1)
	second, err := TaskCleanup(cfg, store, TaskCleanupParams{SessionName: "case-1", Instance: "second"})
	if err != nil || !second.Unsubscribed {
		t.Fatalf("last cleanup = %+v, err = %v", second, err)
	}
	assertTaskRows(0)
}

func TestNodeSubscriptionEndsWithReleasedNode(t *testing.T) {
	store := state.NewStore(t.TempDir())
	resource := &contract.Resource{Type: "item", Name: "one", Locator: "item://one"}
	node := &contract.TaskState{TaskID: "watch", Scope: "session", Status: contract.TaskStatusProduced, Subscription: resource}
	if err := store.Put(&domain.Session{Name: "one", Nodes: map[string]*contract.TaskState{"watch": node}}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListSubscriptions("one", "")
	if err != nil || len(rows) != 1 || rows[0].Source != "node" {
		t.Fatalf("node subscriptions = %+v, err = %v", rows, err)
	}
	cleaned := *node
	cleaned.Status = contract.TaskStatusCleaned
	if err := releaseNodeSubscriptions(nil, store, "one", "", map[string]*contract.TaskState{"watch": &cleaned}); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListSubscriptions("one", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("node subscriptions after release = %+v, err = %v", rows, err)
	}
}

func TestNodeSubscriptionLimitKeepsSetupResultsVisible(t *testing.T) {
	cfg := subscriptionFixture(t, "max_subscribers = 1")
	store := state.NewStore(t.TempDir())
	for _, name := range []string{"holder", "target"} {
		if err := store.Put(&domain.Session{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "holder", ResourceID: "item://one"}); err != nil {
		t.Fatal(err)
	}
	resource, err := ResolveSubscriptionResource(cfg, "", "item://one")
	if err != nil {
		t.Fatal(err)
	}
	watchProcess := &contract.ProcessIdentity{PID: 4242, Start: 1}
	otherProcess := &contract.ProcessIdentity{PID: 4343, Start: 1}
	session := &domain.Session{Name: "target", Nodes: map[string]*contract.TaskState{
		"watch": {TaskID: "watch", Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Subscription: resource, Process: watchProcess, NewExecution: true},
		"other": {TaskID: "other", Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Process: otherProcess, NewExecution: true},
	}}
	err = mergeTasksWithSubscriptions(cfg, store, "target", session, map[string]*contract.Resource{"watch": resource})
	if err == nil || !strings.Contains(err.Error(), "reached max_subscribers (1)") {
		t.Fatalf("merge error = %v, want subscriber limit", err)
	}
	got, err := store.GetE("target")
	if err != nil {
		t.Fatal(err)
	}
	watch := got.Nodes["watch"]
	if watch == nil || watch.Status != contract.TaskStatusFailed || watch.Subscription != nil || !strings.Contains(watch.Error, "reached max_subscribers (1)") || watch.Process == nil || watch.Process.PID != 4242 {
		t.Fatalf("watch after rejected subscription = %+v", watch)
	}
	other := got.Nodes["other"]
	if other == nil || other.Status != contract.TaskStatusProduced || other.Process == nil || other.Process.PID != 4343 {
		t.Fatalf("other after rejected subscription = %+v", other)
	}
	rows, err := store.ListSubscriptions("target", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected subscription rows = %+v, err = %v", rows, err)
	}
	if err := store.Update("target", func(s *domain.Session) error { return nil }); err != nil {
		t.Fatalf("later state write: %v", err)
	}
}

func TestUpPersistsOtherNodesWhenNodeSubscriptionLimitIsReached(t *testing.T) {
	cfg := writeWorkflowFixture(t, "flow",
		[]taskFixture{
			{id: "watch", scope: "session", setup: `printf '{"resource":"item://one"}'`},
			{id: "other", scope: "session", setup: `printf '{"ready":true}'`},
		},
		[]nodeFixture{{id: "watch", subscription: "self.outputs.resource"}, {id: "other"}},
	)
	useResource(t, cfg, "flow", "case", caseResourceDoc)
	writeResourceDoc(t, cfg, "item", `[item]
kind = "resource"
match = '^item://(?P<name>[a-z]+)$'
name = { from = "match.name" }
max_subscribers = 1
[item.observe]
type = "exec"
command = "printf"
args = ['{}']
`)
	store := state.NewStore(t.TempDir())
	if err := store.Put(&domain.Session{Name: "holder"}); err != nil {
		t.Fatal(err)
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "holder", ResourceID: "item://one"}); err != nil {
		t.Fatal(err)
	}
	_, err := Up(cfg, store, UpParams{Identifier: "case://2"})
	if err == nil || !strings.Contains(err.Error(), "reached max_subscribers (1)") {
		t.Fatalf("Up error = %v, want subscriber limit", err)
	}
	got, err := store.GetE("case-2")
	if err != nil || got == nil {
		t.Fatalf("session after failed Up = %+v, err = %v", got, err)
	}
	watch := got.Nodes["watch"]
	if watch == nil || watch.Status != contract.TaskStatusFailed || watch.Subscription != nil || !strings.Contains(watch.Error, "reached max_subscribers (1)") {
		t.Fatalf("watch after failed Up = %+v", watch)
	}
	other := got.Nodes["other"]
	if other == nil || other.Status != contract.TaskStatusProduced || other.Outputs["ready"] != true {
		t.Fatalf("other after failed Up = %+v", other)
	}
}

func TestRuntimeSubscriptionUsesRemovedDeclarationLimit(t *testing.T) {
	cfg := subscriptionFixture(t, "max_subscribers = 1")
	store := state.NewStore(t.TempDir())
	for _, name := range []string{"first", "second"} {
		if err := store.Put(&domain.Session{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "first", ResourceID: "item://one"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.BaseDir, "resources", "item.toml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(original), "max_subscribers = 1", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Subscribe(cfg, store, SubscribeParams{SessionName: "second", ResourceID: "item://one"}); err != nil {
		t.Fatalf("declaration without a limit must admit the second session: %v", err)
	}
}

func TestResolveSubscriptionResourceUsesDeclarationIdentity(t *testing.T) {
	cfg := subscriptionFixture(t, "max_subscribers = 1")
	resource, err := ResolveSubscriptionResource(cfg, "", "item://one")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Type != "item" || resource.Name != "one" || resource.MaxSubscribers != 1 {
		t.Fatalf("resolved resource = %+v", resource)
	}
}
