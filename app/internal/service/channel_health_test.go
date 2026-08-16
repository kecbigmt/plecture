package service

import (
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// setChannelHealth persists ch as the session's open channel-failure streak,
// standing in for what internal/dispatch (recordChannelFailure) does as
// failures actually happen — CheckChannelHealth only reads this, so tests
// exercise its threshold/dedup logic without needing a live dispatcher.
func setChannelHealth(t *testing.T, store interface {
	Update(string, func(*domain.Session) error) error
}, sessionName string, ch *contract.ChannelHealth) {
	t.Helper()
	if err := store.Update(sessionName, func(s *domain.Session) error {
		s.ChannelHealth = ch
		return nil
	}); err != nil {
		t.Fatalf("set channel health: %v", err)
	}
}

func assertChannelEscalations(t *testing.T, dir, target string, want int) []event.Event {
	t.Helper()
	evs, _, _, err := eventlog.NewStore(dir).List(target, 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
	if err != nil {
		t.Fatalf("list channel escalations: %v", err)
	}
	if len(evs) != want {
		t.Fatalf("channel escalation events on %q = %+v, want %d", target, evs, want)
	}
	return evs
}

func TestCheckChannelHealth_NoOpenStreakIsNoop(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("CheckChannelHealth: %v", err)
	}
	if pushed {
		t.Fatal("want no escalation for a session with no open channel-failure streak")
	}
	assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 0)
}

func TestCheckChannelHealth_BelowThresholdDoesNotEscalate(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setChannelHealth(t, store, "owner/repo-1", &contract.ChannelHealth{
		ConsecutiveFailures: 1,
		FirstFailureAt:      time.Now(),
		LastFailureAt:       time.Now(),
		LastKind:            contract.ChannelFailureKindDelivery,
		LastChannel:         "runtime",
		LastError:           "dial unix: connect: connection refused",
	})

	pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("CheckChannelHealth: %v", err)
	}
	if pushed {
		t.Fatal("want no escalation below both the consecutive-failure and age thresholds")
	}
	assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 0)
}

func TestCheckChannelHealth_EscalatesOnceCountThresholdCrossed(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	firstFailure := time.Now().Add(-time.Second)
	setChannelHealth(t, store, "owner/repo-1", &contract.ChannelHealth{
		ConsecutiveFailures: channelHealthFailureThreshold,
		FirstFailureAt:      firstFailure,
		LastFailureAt:       time.Now(),
		LastKind:            contract.ChannelFailureKindDelivery,
		LastChannel:         "runtime",
		LastError:           "dial unix: connect: connection refused",
	})

	pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("CheckChannelHealth: %v", err)
	}
	if !pushed {
		t.Fatal("want an escalation once the consecutive-failure threshold is crossed")
	}
	evs := assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 1)
	meta := evs[0].Metadata
	if meta["escalation_kind"] != "delivery.failed" {
		t.Errorf("escalation_kind = %q, want %q", meta["escalation_kind"], "delivery.failed")
	}
	if meta["channel_failure_kind"] != contract.ChannelFailureKindDelivery {
		t.Errorf("channel_failure_kind = %q", meta["channel_failure_kind"])
	}
	if meta["channel"] != "runtime" {
		t.Errorf("channel = %q, want %q", meta["channel"], "runtime")
	}
	if meta["error"] != "dial unix: connect: connection refused" {
		t.Errorf("error = %q", meta["error"])
	}
	if meta[event.MetaOriginSession] != "owner/repo-1" {
		t.Errorf("origin session metadata = %q, want %q", meta[event.MetaOriginSession], "owner/repo-1")
	}

	// A second check against the same, still-open streak must not re-escalate.
	pushed, err = CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("second CheckChannelHealth: %v", err)
	}
	if pushed {
		t.Fatal("want the already-escalated episode suppressed")
	}
	assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 1)
}

func TestCheckChannelHealth_EscalatesWhenAgeThresholdCrossed(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setChannelHealth(t, store, "owner/repo-1", &contract.ChannelHealth{
		ConsecutiveFailures: 1, // below the count threshold
		FirstFailureAt:      time.Now().Add(-(channelHealthFailureAge + time.Minute)),
		LastFailureAt:       time.Now(),
		LastKind:            contract.ChannelFailureKindValidation,
		LastError:           `event.channel "runtime": uses unknown channel definition "claude_channel"`,
	})

	pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("CheckChannelHealth: %v", err)
	}
	if !pushed {
		t.Fatal("want an escalation once the streak has been open longer than the age threshold")
	}
	evs := assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 1)
	if evs[0].Metadata["channel_failure_kind"] != contract.ChannelFailureKindValidation {
		t.Errorf("channel_failure_kind = %q, want %q", evs[0].Metadata["channel_failure_kind"], contract.ChannelFailureKindValidation)
	}
}

func TestCheckChannelHealth_RecoveryStartsNewEpisode(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil)
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setChannelHealth(t, store, "owner/repo-1", &contract.ChannelHealth{
		ConsecutiveFailures: channelHealthFailureThreshold,
		FirstFailureAt:      time.Now().Add(-time.Minute),
		LastFailureAt:       time.Now(),
		LastKind:            contract.ChannelFailureKindDelivery,
		LastChannel:         "runtime",
		LastError:           "boom",
	})
	if pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1"); err != nil || !pushed {
		t.Fatalf("first CheckChannelHealth: pushed=%v err=%v", pushed, err)
	}
	assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 1)

	// Recovery (internal/dispatch's recordChannelSuccess): clear the streak,
	// ending the episode.
	setChannelHealth(t, store, "owner/repo-1", nil)

	// A new failure starts a fresh episode, free to escalate again.
	setChannelHealth(t, store, "owner/repo-1", &contract.ChannelHealth{
		ConsecutiveFailures: channelHealthFailureThreshold,
		FirstFailureAt:      time.Now(),
		LastFailureAt:       time.Now(),
		LastKind:            contract.ChannelFailureKindDelivery,
		LastChannel:         "runtime",
		LastError:           "boom again",
	})
	pushed, err := CheckChannelHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("second-episode CheckChannelHealth: %v", err)
	}
	if !pushed {
		t.Fatal("want the new episode free to escalate again")
	}
	assertChannelEscalations(t, store.Dir(), "owner/repo-orchestrator", 2)
}
