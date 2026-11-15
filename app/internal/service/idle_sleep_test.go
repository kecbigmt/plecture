package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestSleepIfIdle_CompletedTurnReleasesRunScope(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "default", []taskFixture{{id: "runtime", scope: "run", setup: `echo '{"pid":123}'`, cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[default.session]\nsleep_after_idle = \"10m\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	name := "work-20"
	writeCapWorkflow(t, cfg.BaseDir, "parent", intPtr(1))
	seedSessionWithNodes(t, store, "parent", "acct", 21, "parent", nil)
	seedSessionWithNodes(t, store, name, "acct", 20, "default", nil)
	setParent(t, store, name, "parent")
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveChildCapSlot(cfg, store, "another-child", "parent", false); err == nil {
		t.Fatal("up child did not consume its parent's only slot")
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(time.Hour)); err != nil || slept {
		t.Fatalf("zero-turn session slept = %t, err = %v", slept, err)
	}
	if err := SetMessage(cfg, store, name, "working", nil); err != nil {
		t.Fatal(err)
	}
	if err := SetMessage(cfg, store, name, "", nil); err != nil {
		t.Fatal(err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(5*time.Minute)); err != nil || slept {
		t.Fatalf("before deadline slept = %t, err = %v", slept, err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(11*time.Minute)); err != nil || !slept {
		t.Fatalf("after deadline slept = %t, err = %v", slept, err)
	}
	s := store.Get(name)
	if s.Status != contract.SessionStatusDown || s.SleepSince.IsZero() || cfg.RunScopeUp(s) {
		t.Fatalf("sleep state = %+v", s)
	}
	reserved, capErr := reserveChildCapSlot(cfg, store, "another-child", "parent", false)
	if capErr != nil || !reserved {
		t.Fatalf("sleep did not release parent's slot: reserved = %t, err = %v", reserved, capErr)
	}
	releaseChildCapSlot(store, "another-child")
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(time.Hour)); err != nil || slept {
		t.Fatalf("new run with no completed turn slept from previous run's clear: %t, %v", slept, err)
	}
}

func TestSleepIfIdle_InboundCancelsPendingSleepUntilNextClear(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "default", []taskFixture{{id: "runtime", scope: "run", setup: `echo '{"pid":123}'`, cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[default.session]\nsleep_after_idle = \"10m\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	name := "work-20"
	seedSessionWithNodes(t, store, name, "acct", 20, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if err := SetMessage(cfg, store, name, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{SessionName: name, Type: "user.emit", Direction: event.Inbound, Body: "new work"}); err != nil {
		t.Fatal(err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(time.Hour)); err != nil || slept {
		t.Fatalf("inbound interrupted wait: slept = %t, err = %v", slept, err)
	}
	if err := SetMessage(cfg, store, name, "working", nil); err != nil {
		t.Fatal(err)
	}
	if err := SetMessage(cfg, store, name, "", nil); err != nil {
		t.Fatal(err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(11*time.Minute)); err != nil || !slept {
		t.Fatalf("new clear did not start a deadline: slept = %t, err = %v", slept, err)
	}
}

func TestSleepIfIdle_UnconfiguredWorkflowDoesNotSleep(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "default", []taskFixture{{id: "runtime", scope: "run", setup: `echo '{"pid":123}'`, cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
	name := "work-20"
	seedSessionWithNodes(t, store, name, "acct", 20, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if err := SetMessage(cfg, store, name, "", nil); err != nil {
		t.Fatal(err)
	}
	if slept, err := SleepIfIdle(cfg, store, name, time.Now().Add(time.Hour)); err != nil || slept {
		t.Fatalf("unconfigured workflow slept = %t, err = %v", slept, err)
	}
}

func TestSleepIfIdle_InboundDuringCleanupRequestsWake(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "default", []taskFixture{{id: "runtime", scope: "run", setup: `echo '{"pid":123}'`, cleanup: "sleep 0.3"}}, []nodeFixture{{id: "runtime"}})
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[default.session]\nsleep_after_idle = \"10m\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	name := "work-20"
	seedSessionWithNodes(t, store, name, "acct", 20, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if err := SetMessage(cfg, store, name, "", nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := SleepIfIdle(cfg, store, name, time.Now().Add(time.Hour)); done <- err }()
	deadline := time.Now().Add(time.Second)
	for store.Get(name).Status != contract.SessionStatusDown && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.Get(name).Status != contract.SessionStatusDown {
		t.Fatal("idle sleep never entered cleanup")
	}
	if _, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{SessionName: name, Type: "user.emit", Direction: event.Inbound, Body: "new work"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	queued, err := sleepQueued(store, name)
	if err != nil || !queued {
		t.Fatalf("inbound during cleanup did not request wake: queued=%t err=%v", queued, err)
	}
}
