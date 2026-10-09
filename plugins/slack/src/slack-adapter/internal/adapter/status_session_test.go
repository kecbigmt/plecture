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

func TestSessionStatusLateStreamCannotRestartCompletedTurn(t *testing.T) {
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
	if delivered {
		t.Fatal("late old-turn stream was delivered")
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
}

func (s *sequenceSlack) SetThreadStatus(_, _, status string) error {
	s.events = append(s.events, "status:"+status)
	return nil
}
func (s *sequenceSlack) PostToThread(_, _, _ string) (string, error) {
	s.events = append(s.events, "post")
	return "reply-ts", nil
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
	return nil
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
