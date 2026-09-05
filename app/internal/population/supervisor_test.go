package population

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type restartingQueryRunner struct {
	calls  atomic.Int32
	cancel context.CancelFunc
}

func (r *restartingQueryRunner) Poll(context.Context, Definition) ([]map[string]any, error) {
	return nil, nil
}

func (r *restartingQueryRunner) Subscribe(context.Context, Definition, func(map[string]any) error) error {
	if r.calls.Add(1) == 2 {
		r.cancel()
	}
	return errors.New("source exited")
}

func TestSubscriptionRestartsAfterSourceFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &restartingQueryRunner{cancel: cancel}
	done := make(chan struct{})
	go func() {
		(&Supervisor{}).runSubscription(ctx, runner, Definition{}, make(chan sourceResult, 2))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not restart under bounded backoff")
	}
	if runner.calls.Load() != 2 {
		t.Fatalf("subscribe calls = %d, want restart after first failure", runner.calls.Load())
	}
}
