package adapter

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// fakeStatusSetter records SetThreadStatus calls and lets tests inject a
// failure for the next call.
type fakeStatusSetter struct {
	mu    sync.Mutex
	calls []statusCall
	err   error
}

type statusCall struct {
	channelID, threadTS, status string
	loadingMessages             []string
}

func (f *fakeStatusSetter) SetThreadStatus(channelID, threadTS, status string, loadingMessages []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, statusCall{channelID, threadTS, status, loadingMessages})
	return f.err
}

func (f *fakeStatusSetter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeStatusSetter) last() statusCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func TestStatusManager_Set_ShowsStatus(t *testing.T) {
	setter := &fakeStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", []string{"Reviewing…", "Checking…"}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Set clears before it sets (see StatusManager.Set), so a single Set
	// call produces two SetThreadStatus calls.
	if setter.callCount() != 2 {
		t.Fatalf("SetThreadStatus calls = %d, want 2 (clear, then show)", setter.callCount())
	}
	if got := setter.calls[0]; got.status != "" {
		t.Errorf("first call = %+v, want an empty-status clear", got)
	}
	got := setter.last()
	if got.channelID != "C1" || got.threadTS != "111.0" || got.status != "is thinking…" {
		t.Errorf("show call = %+v, want C1/111.0/is thinking…", got)
	}
	if len(got.loadingMessages) != 2 {
		t.Errorf("loadingMessages = %v, want 2 entries", got.loadingMessages)
	}
}

func TestStatusManager_Clear_ClearsImmediately(t *testing.T) {
	setter := &fakeStatusSetter{}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := mgr.Clear("C1", "111.0"); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	if setter.callCount() != 3 {
		t.Fatalf("SetThreadStatus calls = %d, want 3 (clear, show, clear)", setter.callCount())
	}
	got := setter.last()
	if got.status != "" || got.loadingMessages != nil {
		t.Errorf("clear call = %+v, want empty status and nil loading messages", got)
	}
}

func TestStatusManager_TTL_ClearsWithoutAnyPost(t *testing.T) {
	setter := &fakeStatusSetter{}
	mgr := NewStatusManager(setter, 30*time.Millisecond, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Set's own clear-then-set already accounts for 2 calls; wait for a 3rd
	// (the TTL fallback's clear).
	deadline := time.After(2 * time.Second)
	for setter.callCount() < 3 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for TTL clear")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	got := setter.last()
	if got.status != "" {
		t.Errorf("TTL clear status = %q, want empty", got.status)
	}
}

func TestStatusManager_Set_ResetsTTLTimer(t *testing.T) {
	setter := &fakeStatusSetter{}
	mgr := NewStatusManager(setter, 60*time.Millisecond, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := mgr.Set("C1", "111.0", "still thinking…", nil); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}
	// Original timer would have fired ~20ms from here if it wasn't reset.
	// Each Set is 2 calls (clear, show), so two Sets is 4 so far.
	time.Sleep(40 * time.Millisecond)
	if setter.callCount() != 4 {
		t.Fatalf("SetThreadStatus calls = %d, want 4 (no premature TTL clear)", setter.callCount())
	}

	time.Sleep(60 * time.Millisecond)
	if setter.callCount() != 5 {
		t.Fatalf("SetThreadStatus calls = %d, want 5 (TTL clear after the reset delay)", setter.callCount())
	}
}

func TestStatusManager_Clear_CancelsPendingTTLTimer(t *testing.T) {
	setter := &fakeStatusSetter{}
	mgr := NewStatusManager(setter, 20*time.Millisecond, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := mgr.Clear("C1", "111.0"); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	// Wait past the original TTL window with nothing further happening on
	// the thread. If Clear didn't cancel the timer, it fires a 4th call here.
	time.Sleep(60 * time.Millisecond)
	if got := setter.callCount(); got != 3 {
		t.Fatalf("SetThreadStatus calls = %d, want 3 (clear, show, clear) — stale TTL timer fired again", got)
	}
}

func TestStatusManager_Set_PropagatesSlackError(t *testing.T) {
	wantErr := errors.New("slack down")
	setter := &fakeStatusSetter{err: wantErr}
	mgr := NewStatusManager(setter, time.Hour, testLogger())
	defer mgr.Stop()

	if err := mgr.Set("C1", "111.0", "is thinking…", nil); !errors.Is(err, wantErr) {
		t.Fatalf("Set() error = %v, want %v", err, wantErr)
	}
}

func TestValidateLoadingMessages(t *testing.T) {
	ok := make([]string, 10)
	if err := validateLoadingMessages(ok); err != nil {
		t.Errorf("10 messages should be accepted, got error: %v", err)
	}

	tooMany := make([]string, 11)
	if err := validateLoadingMessages(tooMany); err == nil {
		t.Error("11 messages should be rejected")
	}

	if err := validateLoadingMessages(nil); err != nil {
		t.Errorf("nil should be accepted, got error: %v", err)
	}
}

func TestClipLoadingMessages(t *testing.T) {
	exactly48 := strings.Repeat("a", 48)
	over48 := strings.Repeat("b", 200)

	got := clipLoadingMessages([]string{exactly48, over48})

	if got[0] != exactly48 {
		t.Errorf("48-char entry changed: got %q, want unchanged", got[0])
	}
	if r := []rune(got[1]); len(r) != maxLoadingMessageLen {
		t.Errorf("clipped entry length = %d, want %d", len(r), maxLoadingMessageLen)
	}
	if !strings.HasSuffix(got[1], "…") {
		t.Errorf("clipped entry = %q, want an ellipsis suffix", got[1])
	}
	if !strings.HasPrefix(got[1], strings.Repeat("b", 47)) {
		t.Errorf("clipped entry = %q, want the first 47 source runes preserved", got[1])
	}
}

func TestClipLoadingMessages_NilStaysNil(t *testing.T) {
	if got := clipLoadingMessages(nil); got != nil {
		t.Errorf("clipLoadingMessages(nil) = %v, want nil", got)
	}
}

func TestWriteStatusError_SlackAPIErrorMapsTo422(t *testing.T) {
	w := httptest.NewRecorder()
	writeStatusError(testLogger(), w, slack.SlackErrorResponse{Err: "invalid_arguments"})

	if w.Code != 422 {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"error":"invalid_arguments"`) {
		t.Errorf("body = %q, want it to contain the slack error name", body)
	}
}

func TestWriteStatusError_OtherErrorStays500(t *testing.T) {
	w := httptest.NewRecorder()
	writeStatusError(testLogger(), w, errors.New("dial tcp: connection refused"))

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
