package channel

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/contracts/event"
)

// RetryPolicy bounds a channel worker's delivery attempts. DefaultRetryPolicy is
// the baseline; a channel can shorten an attempt via its own timeout.
type RetryPolicy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Timeout     time.Duration // per-attempt timeout when the channel sets none
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseBackoff: 200 * time.Millisecond,
		MaxBackoff:  2 * time.Second,
		Timeout:     5 * time.Second,
	}
}

// DeliverWithRetry attempts delivery up to policy.MaxAttempts with exponential
// backoff, returning the attempts made and the final error (nil on success).
// Each attempt is bounded by the channel's own timeout, falling back to
// policy.Timeout. A cancelled ctx ends the loop and is returned.
func DeliverWithRetry(ctx context.Context, def config.ChannelDefinition, inputs map[string]any, ev event.Event, policy RetryPolicy) (int, error) {
	return retry(ctx, policy, def.Timeout.Duration, func(attemptCtx context.Context) error {
		return Deliver(attemptCtx, def, inputs, ev)
	})
}

// DeliverWithRetryAndExecutor is DeliverWithRetry with an execution-plane
// override — see DeliverWithExecutor.
func DeliverWithRetryAndExecutor(ctx context.Context, def config.ChannelDefinition, inputs map[string]any, ev event.Event, policy RetryPolicy, executor Executor) (int, error) {
	return retry(ctx, policy, def.Timeout.Duration, func(attemptCtx context.Context) error {
		return DeliverWithExecutor(attemptCtx, def, inputs, ev, executor)
	})
}

// retry runs attempt up to policy.MaxAttempts with exponential backoff, bounding
// each by perAttemptTimeout (or policy.Timeout when unset). Split from
// DeliverWithRetry so the backoff/attempt-count logic is testable without a real
// socket or process.
func retry(ctx context.Context, policy RetryPolicy, perAttemptTimeout time.Duration, attempt func(context.Context) error) (int, error) {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	timeout := perAttemptTimeout
	if timeout <= 0 {
		timeout = policy.Timeout
	}
	backoff := policy.BaseBackoff
	var lastErr error
	for n := 1; n <= policy.MaxAttempts; n++ {
		attemptCtx, cancel := ctx, func() {}
		if timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		err := attempt(attemptCtx)
		cancel()
		if err == nil {
			return n, nil
		}
		lastErr = err
		if n == policy.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if policy.MaxBackoff > 0 && backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
	return policy.MaxAttempts, lastErr
}

// ChannelErrorEvent builds the tws.channel.error event a worker appends after
// exhausting retries. Metadata (channel/event_id/attempts) lets a reader trace
// the failure; it is never a channel `include` target, so it cannot loop.
func ChannelErrorEvent(orig event.Event, channelName string, attempts int, cause error) event.Event {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return event.Event{
		SessionName: orig.SessionName,
		StreamID:    orig.StreamID,
		Type:        event.TypeChannelError,
		Source:      event.SourceTWS,
		Direction:   event.Internal,
		Summary:     fmt.Sprintf("event channel %s failed after %d attempts", channelName, attempts),
		Body:        fmt.Sprintf("failed to deliver %s to channel %s: %s", orig.Type, channelName, reason),
		Metadata: map[string]string{
			"channel":  channelName,
			"event_id": orig.ID,
			"attempts": strconv.Itoa(attempts),
		},
	}
}
