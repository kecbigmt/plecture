package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

var retiredFixtureSpellings = []string{"@workflow", "workspace_provider"}

// retiredRevisionSession brings a session up in the current dialect, then
// rewrites its pinned revision into the shape a previous release stored: a
// workflow naming a workspace provider and the @workflow pseudo-node's
// provider declaration. Like every session that release created, its entry is
// the untyped resource the storage upgrade records for its identifier.
func retiredRevisionSession(t *testing.T, workflowBody string) (*wdFixture, string) {
	t.Helper()
	f := newWDFixture(t, workflowBody)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	name, _ := wdSession(t, f)
	session, err := f.store.GetE(name)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(session.WorkflowRevisionBody), &body); err != nil {
		t.Fatal(err)
	}
	body["workflow"].(map[string]any)["WorkspaceProvider"] = "forge"
	body["provider"] = map[string]any{"ID": "forge"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	session.WorkflowRevision = hex.EncodeToString(sum[:])
	session.WorkflowRevisionBody = string(encoded)
	session.Resource, session.Tag = &contract.Resource{Name: wdIssueURL, Locator: wdIssueURL}, ""
	if err := f.store.Put(session); err != nil {
		t.Fatal(err)
	}
	return f, name
}

func nodeStatuses(nodes map[string]*contract.TaskState) map[string]string {
	out := make(map[string]string, len(nodes))
	for id, node := range nodes {
		if node != nil {
			out[id] = node.Status
		}
	}
	return out
}

func assertRetiredRevisionRefusal(t *testing.T, err error, name string) {
	t.Helper()
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != ErrExecutionFailed {
		t.Fatalf("error = %v, want an execution_failed refusal", err)
	}
	for _, want := range append(slices.Clone(retiredFixtureSpellings), lang.WorkspaceProviderMigrationGuide, "plect destroy "+name, forceRequiresConfirmation) {
		if !strings.Contains(svcErr.Message, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, svcErr.Message)
		}
	}
}

func TestRetiredRevision_LifecycleOperationsStopWithTheMigrationProcedure(t *testing.T) {
	ops := []struct {
		name string
		run  func(f *wdFixture, name string) error
	}{
		{"up by session name", func(f *wdFixture, name string) error {
			_, err := Up(f.cfg, f.store, UpParams{Identifier: name})
			return err
		}},
		{"up by resource", func(f *wdFixture, name string) error {
			_, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL})
			return err
		}},
		{"up --force-recreate", func(f *wdFixture, name string) error {
			_, err := Up(f.cfg, f.store, UpParams{Identifier: name, ForceRecreate: true})
			return err
		}},
		{"down", func(f *wdFixture, name string) error {
			_, err := Down(f.cfg, f.store, DownParams{Identifier: name})
			return err
		}},
		{"down to sleep", func(f *wdFixture, name string) error {
			_, err := Down(f.cfg, f.store, DownParams{Identifier: name, Sleep: true})
			return err
		}},
		{"destroy", func(f *wdFixture, name string) error {
			_, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name})
			return err
		}},
		{"workflow rebase", func(f *wdFixture, name string) error {
			_, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: name})
			return err
		}},
		{"workflow rebase --dry-run", func(f *wdFixture, name string) error {
			_, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: name, DryRun: true})
			return err
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			f, name := retiredRevisionSession(t, wdWorkdirWorkflow)
			before, err := f.store.GetE(name)
			if err != nil {
				t.Fatal(err)
			}
			assertRetiredRevisionRefusal(t, op.run(f, name), name)
			after, err := f.store.GetE(name)
			if err != nil || after == nil {
				t.Fatalf("session after refusal = %v, %v; want it kept", after, err)
			}
			if after.Status != before.Status || after.WorkflowRevision != before.WorkflowRevision {
				t.Errorf("session after refusal = status %q revision %s, want status %q revision %s", after.Status, after.WorkflowRevision, before.Status, before.WorkflowRevision)
			}
			if got, want := nodeStatuses(after.Nodes), nodeStatuses(before.Nodes); !maps.Equal(got, want) {
				t.Errorf("node statuses after refusal = %v, want %v", got, want)
			}
			if _, err := os.Stat(filepath.Join(f.home, "cleanup-intent")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cleanup ran for a retired revision: %v", err)
			}
		})
	}
}

func TestRetiredRevision_DestroyForceDiscardsTheRecordWithoutCleanup(t *testing.T) {
	f, name := retiredRevisionSession(t, wdWorkdirWorkflow)
	result, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name, Force: true})
	if err != nil {
		t.Fatalf("Destroy --force: %v", err)
	}
	if session, err := f.store.GetE(name); err != nil || session != nil {
		t.Fatalf("session after destroy --force = %+v, %v; want the record gone", session, err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "cleanup-intent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup ran from a retired revision: %v", err)
	}
	warned := false
	for _, warning := range result.CleanupWarnings {
		if strings.Contains(warning, "workspace_provider") && strings.Contains(warning, "not verified") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("CleanupWarnings = %q, want a notice that release of the retired revision was not verified", result.CleanupWarnings)
	}
}

func TestRetiredRevision_AutomaticRepairStopsWithTheMigrationProcedure(t *testing.T) {
	f, name := retiredRevisionSession(t, wdWorkdirWorkflow+`
[review.health]
auto_repair_attempts = 2
`)
	if err := f.store.Update(name, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	attempted, err := RepairSession(f.cfg, f.store, name, &HealthReport{Repairable: true, Reason: "agent: alive exited 1"}, NewRepairGate())
	if attempted {
		t.Error("RepairSession attempted a repair from a retired revision")
	}
	assertRetiredRevisionRefusal(t, err, name)
	events, _, _, listErr := eventlog.NewStore(f.store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionRepair}})
	if listErr != nil || len(events) != 0 {
		t.Fatalf("repair events = %+v, %v; want none", events, listErr)
	}
}

func TestRetiredRevision_ListAndStatusNameTheRetiredSpellings(t *testing.T) {
	f, name := retiredRevisionSession(t, wdWorkdirWorkflow)
	entries, err := List(f.cfg, f.store)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	if got := entries[0].RetiredSpellings; !slices.Equal(got, retiredFixtureSpellings) {
		t.Errorf("ListEntry.RetiredSpellings = %v, want %v", got, retiredFixtureSpellings)
	}
	status, err := Status(f.cfg, f.store, name)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := status.Identity.RetiredSpellings; !slices.Equal(got, retiredFixtureSpellings) {
		t.Errorf("status identity RetiredSpellings = %v, want %v", got, retiredFixtureSpellings)
	}
	if got := Summarize(status).Identity.RetiredSpellings; !slices.Equal(got, retiredFixtureSpellings) {
		t.Errorf("status summary identity RetiredSpellings = %v, want %v", got, retiredFixtureSpellings)
	}
}

func TestRetiredRevision_CurrentDialectSessionIsUnaffected(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	name, _ := wdSession(t, f)
	entries, err := List(f.cfg, f.store)
	if err != nil || len(entries) != 1 || entries[0].RetiredSpellings != nil {
		t.Fatalf("List = %+v, %v; want no retired spellings", entries, err)
	}
	if _, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: name, DryRun: true}); err != nil {
		t.Fatalf("workflow rebase --dry-run: %v", err)
	}
	if _, err := Down(f.cfg, f.store, DownParams{Identifier: name}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "cleanup-intent")); err != nil {
		t.Fatalf("destroy of a current-dialect session ran no cleanup: %v", err)
	}
}

func TestRetiredRevision_RefusedDestroyLeavesPendingDeliveryQueued(t *testing.T) {
	f, name := retiredRevisionSession(t, wdWorkdirWorkflow)
	marker := filepath.Join(f.home, "unsubscribed")
	wdWrite(t, filepath.Join(f.base, "resources", "watched.toml"), `
[watched]
kind  = "resource"
match = '^watched://(?P<n>\d+)$'
name  = { expr = "'watched-' + match.n" }

[watched.observe]
type    = "exec"
command = "true"

[watched.subscribe]
type    = "exec"
command = "true"

[watched.unsubscribe]
type    = "exec"
command = "touch"
args    = ["`+marker+`"]
`)
	const resource = "watched://1"
	db, err := retryStore(f.store)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueueSubscriptionRetry(context.Background(), name, retryUnsubscribe, contract.Resource{Type: "watched", Name: "watched-1", Locator: resource}); err != nil {
		t.Fatal(err)
	}
	_, err = Destroy(f.cfg, f.store, DestroyParams{Identifier: name})
	assertRetiredRevisionRefusal(t, err, name)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused destroy ran the queued unsubscribe hook: %v", err)
	}
	if got := subscriptionRetryCount(t, f.store, name, retryUnsubscribe, resource, false); got != 1 {
		t.Errorf("queued unsubscribe retries after a refused destroy = %d, want 1", got)
	}
}
