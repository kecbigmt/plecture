package service

import (
	"os"
	"path/filepath"
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
