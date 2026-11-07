package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/contracts/state"
)

func writeNodeSubscriptionResource(t *testing.T, baseDir, actions string) {
	t.Helper()
	path := filepath.Join(baseDir, "resources", "thread.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[thread_resource]
kind = "resource"
name = "thread"
match = '^thread://'

[thread_resource.observe]
type = "exec"
command = "printf"
args = ['{}']

[thread_resource.state_schema]
type = "object"
` + actions
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNodeSubscriptionBindsProducedOutputAndUnbindsAfterCleanup(t *testing.T) {
	for _, declared := range []bool{false, true} {
		name := "undeclared"
		if declared {
			name = "declared"
		}
		t.Run(name, func(t *testing.T) {
			var subscription string
			if declared {
				subscription = "self.outputs.permalink"
			}
			cfg := writeWorkflowFixture(t, "work", []taskFixture{{
				id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
			}}, []nodeFixture{{id: "thread", subscription: subscription}})
			log := filepath.Join(t.TempDir(), "hooks")
			writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "printf 'subscribe\\n' >> '`+log+`'"

[thread_resource.unsubscribe]
type = "shell"
script = "printf 'unsubscribe\\n' >> '`+log+`'"
`)
			store := testStore(t)
			seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
			if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
				t.Fatalf("Up: %v", err)
			}
			node := store.Get("one").Nodes["thread"]
			if node == nil || node.Status != state.TaskStatusProduced {
				t.Fatalf("node after setup = %+v", node)
			}
			if declared && node.Subscription != "thread://one" {
				t.Fatalf("persisted subscription = %q", node.Subscription)
			}
			if _, err := Down(cfg, store, DownParams{Identifier: "one"}); err != nil {
				t.Fatalf("Down: %v", err)
			}
			data, err := os.ReadFile(log)
			if !declared && os.IsNotExist(err) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(data)); got != "subscribe\nunsubscribe" {
				t.Fatalf("hooks = %q", got)
			}
		})
	}
}

func TestNodeSubscriptionFailedHookQueuesRetry(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "exit 3"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := subscriptionRetryCount(t, store, "one", retrySubscribe, "thread://one", false); got != 1 {
		t.Fatalf("pending subscribe count = %d, want 1", got)
	}
}

func TestNodeSubscriptionRetryRegistersAfterHookRecovers(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	log := filepath.Join(t.TempDir(), "hook")
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "exit 3"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "touch '`+log+`'"
`)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatalf("retrying Up: %v", err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("retry did not run the resource hook: %v", err)
	}
	if got := subscriptionRetryCount(t, store, "one", retrySubscribe, "thread://one", false); got != 0 {
		t.Fatalf("pending retry count = %d", got)
	}
}

func TestNodeSubscriptionCreatedSessionRetriesOnLaterUp(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "session", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	workdir := filepath.Join(t.TempDir(), "wd")
	attachWorkdirDir(t, cfg, "work", workdir)
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "exit 3"
`)
	store := testStore(t)
	created, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/5"})
	if err != nil {
		t.Fatal(err)
	}
	name := created.SessionName
	if got := subscriptionRetryCount(t, store, name, retrySubscribe, "thread://one", false); got != 1 {
		t.Fatalf("pending subscribe count after create = %d, want 1", got)
	}
	log := filepath.Join(t.TempDir(), "hook")
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "touch '`+log+`'"
`)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("later up did not retry the subscribe hook: %v", err)
	}
	if got := subscriptionRetryCount(t, store, name, retrySubscribe, "thread://one", false); got != 0 {
		t.Fatalf("pending subscribe count after retry = %d, want 0", got)
	}
}

func TestNodeSubscriptionQueuedWithProducedStateBeforeHookRuns(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	log := filepath.Join(t.TempDir(), "hook")
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "touch '`+log+`'"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	session := store.Get("one")
	plan, err := buildPlanForSession(cfg, session)
	if err != nil {
		t.Fatal(err)
	}
	wired, err := runNodeSetup(t.Context(), cfg, store, plan.UpOrder(), sessionVars(cfg, session, plan), session, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeTasksWithSubscriptions(cfg, store, "one", session, wired); err != nil {
		t.Fatal(err)
	}
	if got := subscriptionRetryCount(t, store, "one", retrySubscribe, "thread://one", false); got != 1 {
		t.Fatalf("pending subscribe count before hook = %d, want 1", got)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("subscribe hook ran before write-ahead queue: %v", err)
	}
}

func TestNodeSubscriptionKnownRequiredInputsFailRevisionLoad(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`,
		extra: `[outputs_schema]
type = "object"
required = ["permalink"]
[outputs_schema.properties]
permalink = { type = "string", const = "thread://one" }
`,
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "true"
[thread_resource.inputs_schema]
type = "object"
required = ["token"]
[thread_resource.inputs_schema.properties]
token = { type = "string" }
`)
	if _, err := cfg.CaptureWorkflowRevision("", "work"); err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("known invalid subscription should fail revision load: %v", err)
	}
}

func TestNodeSubscriptionRequiredActionInputsAreReported(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "true"

[thread_resource.inputs_schema]
type = "object"
required = ["token"]

[thread_resource.inputs_schema.properties]
token = { type = "string" }
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := subscriptionRetryCount(t, store, "one", retrySubscribe, "thread://one", false); got != 1 {
		t.Fatalf("required inputs were silently skipped: pending subscribe count = %d", got)
	}
	if errs := flushPendingDelivery(cfg, store, "one"); len(errs) != 1 || !strings.Contains(errs[0].Error(), "inputs") {
		t.Fatalf("retry errors = %v, want a reported input failure", errs)
	}
	if got := subscriptionRetryCount(t, store, "one", retrySubscribe, "thread://one", false); got != 1 {
		t.Fatalf("invalid node subscription retry was removed: count = %d", got)
	}
}

func TestNodeSubscriptionSharedWithEntryResourceSurvivesCleanup(t *testing.T) {
	const resource = "thread://one"
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"` + resource + `"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	log := filepath.Join(t.TempDir(), "hooks")
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "true"
[thread_resource.unsubscribe]
type = "shell"
script = "touch '`+log+`'"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if err := store.Update("one", func(s *domain.Session) error {
		s.Resource = testResource(resource)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("shared entry subscription was removed: %v", err)
	}
}

func TestNodeSubscriptionRebuildUnsubscribesBeforeResubscribe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "alive")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true", alive: "test -f " + marker,
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	log := filepath.Join(t.TempDir(), "hooks")
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "printf 'subscribe\\n' >> '`+log+`'"
[thread_resource.unsubscribe]
type = "shell"
script = "printf 'unsubscribe\\n' >> '`+log+`'"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "subscribe\nunsubscribe\nsubscribe" {
		t.Fatalf("hooks = %q", got)
	}
}

func TestNodeSubscriptionUnsubscribeFailureQueuesRetry(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{"permalink":"thread://one"}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	writeNodeSubscriptionResource(t, cfg.BaseDir, `
[thread_resource.subscribe]
type = "shell"
script = "true"
[thread_resource.unsubscribe]
type = "shell"
script = "exit 3"
`)
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "one"}); err != nil {
		t.Fatal(err)
	}
	if got := subscriptionRetryCount(t, store, "one", retryUnsubscribe, "thread://one", false); got != 1 {
		t.Fatalf("pending unsubscribe count = %d", got)
	}
}

func TestNodeSubscriptionMissingOutputFailsSetup(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{{
		id: "thread", scope: "run", setup: `echo '{}'`, cleanup: "true",
	}}, []nodeFixture{{id: "thread", subscription: "self.outputs.permalink"}})
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: "one"}); err == nil || !strings.Contains(err.Error(), "subscription output") {
		t.Fatalf("missing subscription output error = %v", err)
	}
}

func TestRunNodeSetupReportsBothSetupAndSubscriptionErrors(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{
		{id: "bad", scope: "run", setup: `echo '{}'`, cleanup: "true"},
		{id: "broken", scope: "run", setup: "echo setup-broke >&2; exit 1", cleanup: "true"},
	}, []nodeFixture{
		{id: "bad", subscription: "self.outputs.permalink"},
		{id: "broken"},
	})
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	session := store.Get("one")
	plan, err := buildPlanForSession(cfg, session)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runNodeSetup(t.Context(), cfg, store, plan.UpOrder(), sessionVars(cfg, session, plan), session, nil)
	if err == nil {
		t.Fatal("runNodeSetup returned no error")
	}
	if !strings.Contains(err.Error(), "subscription output") {
		t.Errorf("subscription error missing from %q", err)
	}
	if !strings.Contains(err.Error(), `"broken"`) {
		t.Errorf("setup error missing from %q", err)
	}
}

func TestRunNodeSetupWiresRemainingNodesWhenOneSubscriptionOutputIsInvalid(t *testing.T) {
	cfg := writeWorkflowFixture(t, "work", []taskFixture{
		{id: "bad", scope: "run", setup: `echo '{}'`, cleanup: "true"},
		{id: "good", scope: "run", setup: `echo '{"permalink":"thread://good"}'`, cleanup: "true"},
		{id: "blank", scope: "run", setup: `echo '{"permalink":"  "}'`, cleanup: "true"},
		{id: "other", scope: "run", setup: `echo '{"permalink":"thread://other"}'`, cleanup: "true"},
	}, []nodeFixture{
		{id: "bad", subscription: "self.outputs.permalink"},
		{id: "good", subscription: "self.outputs.permalink"},
		{id: "blank", subscription: "self.outputs.permalink"},
		{id: "other", subscription: "self.outputs.permalink"},
	})
	store := testStore(t)
	seedSessionWithNodes(t, store, "one", "org/repo", 1, "work", nil)
	session := store.Get("one")
	plan, err := buildPlanForSession(cfg, session)
	if err != nil {
		t.Fatal(err)
	}
	wired, err := runNodeSetup(t.Context(), cfg, store, plan.UpOrder(), sessionVars(cfg, session, plan), session, nil)
	if err == nil || !strings.Contains(err.Error(), `"bad"`) || !strings.Contains(err.Error(), `"blank"`) {
		t.Fatalf("want both invalid nodes reported, got %v", err)
	}
	want := map[string]string{"good": "thread://good", "other": "thread://other"}
	if len(wired) != len(want) {
		t.Fatalf("wired = %v, want %v", wired, want)
	}
	for id, resource := range want {
		if wired[id] != resource {
			t.Errorf("wired[%q] = %q, want %q (wired = %v)", id, wired[id], resource, wired)
		}
	}
}
