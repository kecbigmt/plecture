package commands

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/inbox"
	"github.com/kecbigmt/plecture/contracts/event"
)

const inboxTestSession = "session-1"

type takenBatch struct {
	Batch      string        `json:"batch"`
	Deliveries int           `json:"deliveries"`
	Events     []event.Event `json:"events"`
}

func inboxTestStores(t *testing.T) (*eventlog.Store, *inbox.Store) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("PLECT_DATA_HOME", dataDir)
	t.Setenv("PLECT_CONFIG_HOME", t.TempDir())
	oldWait := inboxTakeWait
	t.Cleanup(func() { inboxTakeWait = oldWait })
	inboxTakeWait = 0
	return eventlog.NewStore(dataDir), inbox.NewStore(dataDir)
}

func handOverForTest(t *testing.T, log *eventlog.Store, store *inbox.Store, id, supersedes string) {
	t.Helper()
	_, seq, _, err := log.Append(event.Event{ID: id, SessionName: inboxTestSession, Type: event.TypeUserEmit, Direction: event.Inbound, Body: "body of " + id, Supersedes: supersedes})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(inboxTestSession, []int64{seq}, "runtime"); err != nil {
		t.Fatal(err)
	}
}

func runInboxTake(t *testing.T) (takenBatch, string) {
	t.Helper()
	var out bytes.Buffer
	inboxTakeCmd.SetOut(&out)
	t.Cleanup(func() { inboxTakeCmd.SetOut(nil) })
	if err := inboxTakeCmd.RunE(inboxTakeCmd, []string{inboxTestSession}); err != nil {
		t.Fatalf("inbox take: %v", err)
	}
	var batch takenBatch
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &batch); err != nil {
			t.Fatalf("inbox take output is not a JSON batch: %v\n%s", err, out.String())
		}
	}
	return batch, out.String()
}

func TestInboxTakeCommand_PrintsThePendingBatchWithSupersededEventsOmitted(t *testing.T) {
	log, store := inboxTestStores(t)
	handOverForTest(t, log, store, "kick-1", "")
	handOverForTest(t, log, store, "kick-2", "kick-1")

	batch, _ := runInboxTake(t)

	if batch.Batch == "" || batch.Deliveries != 1 {
		t.Fatalf("batch = %+v, want an id and one delivery", batch)
	}
	if len(batch.Events) != 1 || batch.Events[0].ID != "kick-2" || batch.Events[0].Body != "body of kick-2" {
		t.Fatalf("events = %+v, want only kick-2 with its body", batch.Events)
	}
}

func TestInboxTakeCommand_PrintsNothingWhenNothingIsPending(t *testing.T) {
	log, _ := inboxTestStores(t)
	if _, _, _, err := log.Append(event.Event{SessionName: inboxTestSession, Type: event.TypeUserNote, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}

	if _, raw := runInboxTake(t); raw != "" {
		t.Fatalf("output = %q, want empty", raw)
	}
}

func TestInboxTakeCommand_WaitReturnsWhenAnEntryArrives(t *testing.T) {
	log, store := inboxTestStores(t)
	if _, _, _, err := log.Append(event.Event{SessionName: inboxTestSession, Type: event.TypeUserNote, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}
	inboxTakeWait = 30 * time.Second
	go func() {
		time.Sleep(100 * time.Millisecond)
		handOverForTest(t, log, store, "late", "")
	}()

	start := time.Now()
	batch, _ := runInboxTake(t)

	if len(batch.Events) != 1 || batch.Events[0].ID != "late" {
		t.Fatalf("events = %+v, want [late]", batch.Events)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("take returned after %s, want soon after the entry arrived", elapsed)
	}
}

func TestInboxTakeCommand_WaitGivesUpWithNothingPending(t *testing.T) {
	log, _ := inboxTestStores(t)
	if _, _, _, err := log.Append(event.Event{SessionName: inboxTestSession, Type: event.TypeUserNote, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}
	inboxTakeWait = 300 * time.Millisecond

	if _, raw := runInboxTake(t); raw != "" {
		t.Fatalf("output = %q, want empty after the wait", raw)
	}
}

func TestInboxAckCommand_ReleasesTheBatchAndRejectsARepeat(t *testing.T) {
	log, store := inboxTestStores(t)
	handOverForTest(t, log, store, "first", "")
	batch, _ := runInboxTake(t)

	if err := inboxAckCmd.RunE(inboxAckCmd, []string{inboxTestSession, batch.Batch}); err != nil {
		t.Fatalf("inbox ack: %v", err)
	}

	if _, raw := runInboxTake(t); raw != "" {
		t.Fatalf("take after ack = %q, want empty", raw)
	}
	if err := inboxAckCmd.RunE(inboxAckCmd, []string{inboxTestSession, batch.Batch}); err == nil {
		t.Fatal("second ack of the same batch succeeded")
	}
}

func TestInboxTakeCommand_RedeliversAfterAConsumerCrash(t *testing.T) {
	log, store := inboxTestStores(t)
	handOverForTest(t, log, store, "first", "")
	taken, _ := runInboxTake(t)

	// No ack: the consumer died. Its replacement takes again.
	again, _ := runInboxTake(t)

	if again.Batch != taken.Batch || again.Deliveries != 2 {
		t.Fatalf("second take = %+v, want batch %s delivered twice", again, taken.Batch)
	}
	if len(again.Events) != 1 || again.Events[0].ID != "first" {
		t.Fatalf("redelivered events = %+v, want [first]", again.Events)
	}
}

func TestInboxCommands_HonorTheSessionGuardOfTheirProcess(t *testing.T) {
	log, store := inboxTestStores(t)
	handOverForTest(t, log, store, "first", "")
	taken, _ := runInboxTake(t)
	t.Setenv("PLECT_SESSION_GUARD", "^acme/")

	if err := inboxTakeCmd.RunE(inboxTakeCmd, []string{inboxTestSession}); err == nil {
		t.Fatal("take of a session outside the guard succeeded")
	}
	if err := inboxAckCmd.RunE(inboxAckCmd, []string{inboxTestSession, taken.Batch}); err == nil {
		t.Fatal("ack of a session outside the guard succeeded")
	}

	t.Setenv("PLECT_SESSION_GUARD", "")
	again, _ := runInboxTake(t)
	if again.Batch != taken.Batch || again.Deliveries != 2 {
		t.Fatalf("take after the rejected calls = %+v, want batch %s on its second delivery", again, taken.Batch)
	}
}
