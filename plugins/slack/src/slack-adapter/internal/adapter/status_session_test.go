package adapter

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type sessionStatusCall struct{ channel, thread, status string }

type sessionStatusSetter struct {
	mu    sync.Mutex
	calls []sessionStatusCall
	err   error
}

func (s *sessionStatusSetter) SetThreadStatus(channel, thread, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sessionStatusCall{channel, thread, status})
	return s.err
}

func (s *sessionStatusSetter) snapshot() []sessionStatusCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sessionStatusCall(nil), s.calls...)
}

func TestSessionStatusUsesTurnCompletionAndRejectsStaleCompletion(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	for _, step := range []struct {
		begin bool
		turn  string
	}{
		{true, "turn-1"}, {true, "turn-2"}, {false, "turn-1"}, {false, "turn-2"},
	} {
		var err error
		if step.begin {
			err = mgr.Begin("C1", "T1", step.turn)
		} else {
			err = mgr.End("C1", "T1", step.turn)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []sessionStatusCall{{"C1", "T1", "processing"}, {"C1", "T1", "processing"}, {"C1", "T1", "active"}}
	got := setter.snapshot()
	if len(got) != len(want) {
		t.Fatalf("status calls = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("status call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSessionStatusLatePreviousTurnActivityDoesNotReplaceCurrentTurn(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.End("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Begin("C1", "T1", "new"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Begin("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.End("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if got := setter.snapshot(); len(got) != 3 || got[2].status != "processing" {
		t.Fatalf("status calls = %+v, want current turn still processing", got)
	}
}

func TestSessionStatusLateContentIsDeliveredWithoutRestartingCompletedTurn(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.End("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	delivered := false
	if err := mgr.Deliver("C1", "T1", "old", func() error { delivered = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("late old-turn content was discarded")
	}
	if got := setter.snapshot(); len(got) != 2 || got[1].status != "active" {
		t.Fatalf("status calls = %+v, want completed turn to stay active", got)
	}
}

func TestSessionStatusLateContentPreservesNewTurnProcessing(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Begin("C1", "T1", "new"); err != nil {
		t.Fatal(err)
	}
	delivered := false
	if err := mgr.Deliver("C1", "T1", "old", func() error { delivered = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("late old-turn content was discarded")
	}
	if got := setter.snapshot(); len(got) != 2 || got[1].status != "processing" {
		t.Fatalf("status calls = %+v, want new turn to stay processing", got)
	}
}

func TestSessionStatusIsIsolatedByChannelAndThread(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Begin("C2", "T1", "new"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.End("C1", "T1", "old"); err != nil {
		t.Fatal(err)
	}
	got := setter.snapshot()
	if len(got) != 3 || got[2] != (sessionStatusCall{"C1", "T1", "active"}) {
		t.Fatalf("status calls = %+v", got)
	}
}

func TestSessionStatusOverdueDoesNotClearProcessing(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, 10*time.Millisecond, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(35 * time.Millisecond)
	if got := setter.snapshot(); len(got) != 1 {
		t.Fatalf("overdue status calls = %+v, want only processing", got)
	}
}

func TestSessionStatusCompletionAfterAdapterRestartSetsActive(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.End("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	if got := setter.snapshot(); len(got) != 1 || got[0].status != "active" {
		t.Fatalf("status calls = %+v, want active after restored turn", got)
	}
}

func TestSessionStatusFailedTransitionCanBeRetried(t *testing.T) {
	setter := &sessionStatusSetter{err: errors.New("offline")}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "turn"); err == nil {
		t.Fatal("expected error")
	}
	setter.mu.Lock()
	setter.err = nil
	setter.mu.Unlock()
	if err := mgr.Begin("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.End("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	if got := setter.snapshot(); len(got) != 3 || got[2].status != "active" {
		t.Fatalf("status calls = %+v", got)
	}
}

func TestSessionStatusFailedCompletionCanBeRetried(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	setter.mu.Lock()
	setter.err = errors.New("offline")
	setter.mu.Unlock()
	if err := mgr.End("C1", "T1", "turn"); err == nil {
		t.Fatal("expected error")
	}
	setter.mu.Lock()
	setter.err = nil
	setter.mu.Unlock()
	if err := mgr.End("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	if got := setter.snapshot(); len(got) != 3 || got[1].status != "active" || got[2].status != "active" {
		t.Fatalf("status calls = %+v, want active retried", got)
	}
}

func TestSessionStatusWaitsForOutboundDelivery(t *testing.T) {
	setter := &sessionStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()
	if err := mgr.Begin("C1", "T1", "turn"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan struct{})
	go func() {
		_ = mgr.Deliver("C1", "T1", "turn", func() error {
			close(started)
			<-release
			return nil
		})
		close(delivered)
	}()
	<-started
	ended := make(chan error, 1)
	go func() { ended <- mgr.End("C1", "T1", "turn") }()
	select {
	case err := <-ended:
		t.Fatalf("turn ended before delivery: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-delivered
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	if got := setter.snapshot(); len(got) != 2 || got[0].status != "processing" || got[1].status != "active" {
		t.Fatalf("status calls = %+v", got)
	}
}

type sequenceSlack struct {
	events   []string
	startErr error
	postErr  error
	stopErr  error
}

func (s *sequenceSlack) SetThreadStatus(_, _, status string) error {
	s.events = append(s.events, "status:"+status)
	return nil
}
func (s *sequenceSlack) PostToThread(_, _, _ string) (string, error) {
	s.events = append(s.events, "post")
	return "reply-ts", s.postErr
}
func (s *sequenceSlack) StartStream(_, _, _, _, _ string) (string, error) {
	s.events = append(s.events, "start")
	if s.startErr != nil {
		return "", s.startErr
	}
	return "stream-ts", nil
}
func (s *sequenceSlack) AppendStream(_, _, _ string) error {
	s.events = append(s.events, "append")
	return nil
}
func (s *sequenceSlack) StopStream(_, _, _, status string) error {
	s.events = append(s.events, "stop:"+status)
	return s.stopErr
}

func TestSessionStatusCompletesAfterNativeOrFallbackAnswer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		startErr error
		want     []string
	}{
		{"native", nil, []string{"status:processing", "start", "stop:processing", "status:active"}},
		{"fallback", errors.New("unsupported"), []string{"status:processing", "start", "post", "status:active"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &sequenceSlack{startErr: tc.startErr}
			status := NewStatusManager(api, time.Hour, testLogger())
			defer status.Stop()
			stream := NewStreamManager(api, api, testLogger())
			stream.RecordRecipient("C1", "T1", "U1", "TEAM")
			if err := status.Begin("C1", "T1", "turn"); err != nil {
				t.Fatal(err)
			}
			if err := status.Deliver("C1", "T1", "turn", func() error {
				return stream.Deliver("C1", "T1", "msg", "turn", 0, "answer", true)
			}); err != nil {
				t.Fatal(err)
			}
			if err := status.End("C1", "T1", "turn"); err != nil {
				t.Fatal(err)
			}
			if len(api.events) != len(tc.want) {
				t.Fatalf("events = %v, want %v", api.events, tc.want)
			}
			for i := range tc.want {
				if api.events[i] != tc.want[i] {
					t.Errorf("event %d = %q, want %q", i, api.events[i], tc.want[i])
				}
			}
		})
	}
}

func TestSessionStatusDeliversLateAnswerWithoutChangingCurrentStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		started    bool
		newTurn    bool
		wantEvents []string
	}{
		{"completed turn, no stream", false, false, []string{"status:processing", "status:active", "post"}},
		{"completed turn, existing stream", true, false, []string{"status:processing", "start", "status:active", "stop:active"}},
		{"new turn, no old stream", false, true, []string{"status:processing", "status:processing", "post"}},
		{"new turn, existing old stream", true, true, []string{"status:processing", "start", "status:processing", "stop:processing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &sequenceSlack{}
			status := NewStatusManager(api, time.Hour, testLogger())
			defer status.Stop()
			stream := NewStreamManager(api, api, testLogger())
			stream.RecordRecipient("C1", "T1", "U1", "TEAM")
			if err := status.Begin("C1", "T1", "old"); err != nil {
				t.Fatal(err)
			}
			index := int64(0)
			if tc.started {
				if err := status.Deliver("C1", "T1", "old", func() error {
					return stream.Deliver("C1", "T1", "msg", "old", 0, "first ", false)
				}); err != nil {
					t.Fatal(err)
				}
				index = 1
			}
			if tc.newTurn {
				if err := status.Begin("C1", "T1", "new"); err != nil {
					t.Fatal(err)
				}
			} else if err := status.End("C1", "T1", "old"); err != nil {
				t.Fatal(err)
			}
			if err := status.DeliveryContext("C1", "T1", "old", func(late bool, currentStatus string) error {
				if !late {
					t.Fatal("old-turn answer should be late")
				}
				return stream.DeliverLate("C1", "T1", "msg", "old", index, "answer", true, currentStatus)
			}); err != nil {
				t.Fatal(err)
			}
			if len(api.events) != len(tc.wantEvents) {
				t.Fatalf("events = %v, want %v", api.events, tc.wantEvents)
			}
			for i, want := range tc.wantEvents {
				if api.events[i] != want {
					t.Errorf("event %d = %q, want %q", i, api.events[i], want)
				}
			}
		})
	}
}

func TestSessionStatusRetriesFailedLateAnswerWithoutChangingCurrentStatus(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "native"}[started], func(t *testing.T) {
			api := &sequenceSlack{}
			status := NewStatusManager(api, time.Hour, testLogger())
			defer status.Stop()
			stream := NewStreamManager(api, api, testLogger())
			stream.RecordRecipient("C1", "T1", "U1", "TEAM")
			if err := status.Begin("C1", "T1", "old"); err != nil {
				t.Fatal(err)
			}
			index := int64(0)
			if started {
				if err := stream.Deliver("C1", "T1", "msg", "old", 0, "first ", false); err != nil {
					t.Fatal(err)
				}
				index = 1
				api.stopErr = errors.New("offline")
			} else {
				api.postErr = errors.New("offline")
			}
			if err := status.End("C1", "T1", "old"); err != nil {
				t.Fatal(err)
			}
			deliver := func() error {
				return status.DeliveryContext("C1", "T1", "old", func(late bool, currentStatus string) error {
					if !late {
						t.Fatal("expected late delivery")
					}
					return stream.DeliverLate("C1", "T1", "msg", "old", index, "answer", true, currentStatus)
				})
			}
			if err := deliver(); err == nil {
				t.Fatal("failed late delivery returned success")
			}
			api.postErr, api.stopErr = nil, nil
			if err := deliver(); err != nil {
				t.Fatal(err)
			}
			if got := api.events[len(api.events)-1]; got != map[bool]string{false: "post", true: "stop:active"}[started] {
				t.Fatalf("last event = %q", got)
			}
			if got := api.events[len(api.events)-3]; got != "status:active" {
				t.Fatalf("status was changed by late retry: %v", api.events)
			}
		})
	}
}
