package channel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/contracts/event"
)

func fastPolicy(maxAttempts int) RetryPolicy {
	return RetryPolicy{MaxAttempts: maxAttempts, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Timeout: time.Second}
}

func TestRetry_SucceedsFirstAttempt(t *testing.T) {
	calls := 0
	attempts, err := retry(context.Background(), fastPolicy(3), 0, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil || attempts != 1 || calls != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v, want 1/1/nil", attempts, calls, err)
	}
}

func TestRetry_RecoversAfterFailures(t *testing.T) {
	calls := 0
	attempts, err := retry(context.Background(), fastPolicy(5), 0, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("flaky")
		}
		return nil
	})
	if err != nil || attempts != 3 || calls != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v, want 3/3/nil", attempts, calls, err)
	}
}

func TestRetry_Exhausts(t *testing.T) {
	calls := 0
	want := errors.New("always")
	attempts, err := retry(context.Background(), fastPolicy(3), 0, func(context.Context) error {
		calls++
		return want
	})
	if attempts != 3 || calls != 3 || !errors.Is(err, want) {
		t.Fatalf("attempts=%d calls=%d err=%v, want 3/3/%v", attempts, calls, err, want)
	}
}

func TestRetry_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	attempts, err := retry(ctx, RetryPolicy{MaxAttempts: 5, BaseBackoff: time.Hour, Timeout: time.Second}, 0, func(context.Context) error {
		calls++
		cancel() // cancel during the first attempt; the backoff wait should abort
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 || calls != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v, want 1/1/context.Canceled", attempts, calls, err)
	}
}

func TestRetry_PerAttemptTimeoutCutsAttempt(t *testing.T) {
	calls, deadlines := 0, 0
	// perAttemptTimeout (5ms) must bound each attempt even though policy.Timeout
	// is an hour, and the loop must keep retrying after a timed-out attempt.
	policy := RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Timeout: time.Hour}
	attempts, err := retry(context.Background(), policy, 5*time.Millisecond, func(ctx context.Context) error {
		calls++
		if _, ok := ctx.Deadline(); ok {
			deadlines++
		}
		<-ctx.Done() // block until the per-attempt timeout fires
		return ctx.Err()
	})
	if attempts != 2 || calls != 2 || deadlines != 2 || err == nil {
		t.Fatalf("attempts=%d calls=%d deadlines=%d err=%v, want 2/2/2/non-nil", attempts, calls, deadlines, err)
	}
}

func TestDeliverWithRetry_ExhaustsOnBadSocket(t *testing.T) {
	def := config.ChannelDefinition{
		Type: config.ChannelTypeUnixSocket,
		Path: filepath.Join(t.TempDir(), "absent.sock"),
		Body: "{{ json .Event }}",
	}
	attempts, err := DeliverWithRetry(context.Background(), def, nil, event.Event{Type: event.TypeInstruction}, fastPolicy(3))
	if err == nil {
		t.Fatal("expected error delivering to a non-existent socket")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestChannelErrorEvent(t *testing.T) {
	orig := event.Event{ID: "01ABC", SessionName: "o/r-1", Type: event.TypeInstruction}
	ce := ChannelErrorEvent(orig, "runtime", 3, errors.New("tmux exited 1"))
	if ce.Type != event.TypeChannelError {
		t.Errorf("Type = %q, want %q", ce.Type, event.TypeChannelError)
	}
	if ce.Source != event.SourcePlecture || ce.Direction != event.Internal {
		t.Errorf("source/direction = %q/%q", ce.Source, ce.Direction)
	}
	if ce.SessionName != "o/r-1" {
		t.Errorf("session not carried: %+v", ce)
	}
	if ce.Metadata["channel"] != "runtime" || ce.Metadata["event_id"] != "01ABC" || ce.Metadata["attempts"] != "3" {
		t.Errorf("metadata = %+v", ce.Metadata)
	}
}

func TestChannelErrorEvent_NilCause(t *testing.T) {
	ce := ChannelErrorEvent(event.Event{ID: "x", Type: event.TypeInstruction}, "runtime", 1, nil)
	if ce.Type != event.TypeChannelError || ce.Metadata["attempts"] != "1" {
		t.Errorf("nil-cause error event malformed: %+v", ce)
	}
}
