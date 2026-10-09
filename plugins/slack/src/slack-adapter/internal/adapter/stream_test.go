package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type recordingStreamer struct {
	mu sync.Mutex

	startCalls  []startCall
	appendCalls []appendCall
	stopCalls   []stopCall

	startErr  error
	appendErr error
	stopErr   error

	nextTS int
}

type startCall struct {
	channelID, threadTS, teamID, recipientUserID, text string
}

type appendCall struct {
	channelID, ts, text string
}

type stopCall struct {
	channelID, ts, text string
}

func (f *recordingStreamer) StartStream(channelID, threadTS, teamID, recipientUserID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, startCall{channelID, threadTS, teamID, recipientUserID, text})
	if f.startErr != nil {
		return "", f.startErr
	}
	f.nextTS++
	return fmt.Sprintf("ts-%d", f.nextTS), nil
}

func (f *recordingStreamer) AppendStream(channelID, ts, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendCalls = append(f.appendCalls, appendCall{channelID, ts, text})
	return f.appendErr
}

func (f *recordingStreamer) StopStream(channelID, ts, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, stopCall{channelID, ts, text})
	return f.stopErr
}

func TestStreamManager_InOrderChunks_StartsAppendsAndStops(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world", false); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 2, "!", true); err != nil {
		t.Fatalf("chunk 2 (final): %v", err)
	}

	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1", len(streamer.startCalls))
	}
	start := streamer.startCalls[0]
	if start.channelID != "C1" || start.threadTS != "111.0" || start.teamID != "T1" || start.recipientUserID != "U1" || start.text != "Hello" {
		t.Errorf("StartStream call = %+v, want C1/111.0/T1/U1/Hello", start)
	}

	if len(streamer.appendCalls) != 1 {
		t.Fatalf("AppendStream calls = %d, want 1", len(streamer.appendCalls))
	}
	if got := streamer.appendCalls[0]; got.text != ", world" {
		t.Errorf("AppendStream text = %q, want %q", got.text, ", world")
	}

	if len(streamer.stopCalls) != 1 {
		t.Fatalf("StopStream calls = %d, want 1", len(streamer.stopCalls))
	}
	if got := streamer.stopCalls[0]; got.text != "!" {
		t.Errorf("StopStream text = %q, want %q (only the final delta)", got.text, "!")
	}

	if len(poster.calls) != 0 {
		t.Errorf("PostToThread calls = %d, want 0 (native streaming succeeded)", len(poster.calls))
	}
}

func TestStreamManager_SingleChunkFinal_SeedsStartThenStopsWithNoFurtherText(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if len(streamer.startCalls) != 1 || streamer.startCalls[0].text != "Hello" {
		t.Fatalf("StartStream calls = %+v, want one call seeded with Hello", streamer.startCalls)
	}
	if len(streamer.stopCalls) != 1 || streamer.stopCalls[0].text != "" {
		t.Fatalf("StopStream calls = %+v, want one call with empty text (avoid double-posting the seed)", streamer.stopCalls)
	}
	if len(streamer.appendCalls) != 0 {
		t.Errorf("AppendStream calls = %d, want 0", len(streamer.appendCalls))
	}
}

func TestStreamManager_OutOfOrderChunks_BufferUntilGapCloses(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, "world", false); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if len(streamer.startCalls) != 0 || len(streamer.appendCalls) != 0 {
		t.Fatalf("out-of-order chunk reached Slack before its gap closed: start=%v append=%v",
			streamer.startCalls, streamer.appendCalls)
	}

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "hello ", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if len(streamer.startCalls) != 1 || streamer.startCalls[0].text != "hello " {
		t.Fatalf("StartStream calls = %+v, want one call seeded with 'hello '", streamer.startCalls)
	}
	if len(streamer.appendCalls) != 1 || streamer.appendCalls[0].text != "world" {
		t.Fatalf("AppendStream calls = %+v, want one call appending 'world'", streamer.appendCalls)
	}
}

func TestStreamManager_BufferBoundExceeded_FlushesDespiteGap(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	for i := int64(1); i <= maxPendingStreamChunks; i++ {
		if err := mgr.Deliver("C1", "111.0", "msg-1", "", i, "x", false); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}

	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1 (flush proceeded despite the missing index 0)", len(streamer.startCalls))
	}
	if got, want := len(streamer.appendCalls), maxPendingStreamChunks-1; got != want {
		t.Fatalf("AppendStream calls = %d, want %d", got, want)
	}
}

func TestStreamManager_StartFailure_FallsBackToOnePostOnFinal(t *testing.T) {
	streamer := &recordingStreamer{startErr: errors.New("streaming not enabled for this app")}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world", false); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	if len(poster.calls) != 0 {
		t.Fatalf("PostToThread calls = %d, want 0 before final", len(poster.calls))
	}

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 2, "!", true); err != nil {
		t.Fatalf("chunk 2 (final): %v", err)
	}

	if len(streamer.appendCalls) != 0 || len(streamer.stopCalls) != 0 {
		t.Errorf("AppendStream/StopStream should never be called once StartStream failed: append=%v stop=%v",
			streamer.appendCalls, streamer.stopCalls)
	}
	if len(poster.calls) != 1 {
		t.Fatalf("PostToThread calls = %d, want 1", len(poster.calls))
	}
	if got, want := poster.calls[0].Text, "Hello, world!"; got != want {
		t.Errorf("fallback post text = %q, want %q (full accumulated text)", got, want)
	}
}

func TestStreamManager_AppendFailure_PreservesChunkForRetry(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	streamer.appendErr = errors.New("temporary network error")
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world", false); err == nil {
		t.Fatal("Deliver should surface the append failure, not silently succeed")
	}
	if len(streamer.appendCalls) != 1 {
		t.Fatalf("AppendStream calls = %d, want 1 (the failed attempt)", len(streamer.appendCalls))
	}

	streamer.appendErr = nil
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world", false); err != nil {
		t.Fatalf("retry of chunk 1: %v", err)
	}
	if len(streamer.appendCalls) != 2 {
		t.Fatalf("AppendStream calls = %d, want 2 (the retry resent the same chunk)", len(streamer.appendCalls))
	}

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 2, "!", true); err != nil {
		t.Fatalf("chunk 2 (final): %v", err)
	}
	if len(streamer.stopCalls) != 1 || streamer.stopCalls[0].text != "!" {
		t.Fatalf("StopStream calls = %+v, want one call with '!'", streamer.stopCalls)
	}
}

func TestStreamManager_FallbackPostFailure_RetriesWithoutDuplicatingText(t *testing.T) {
	streamer := &recordingStreamer{startErr: errors.New("streaming not enabled for this app")}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	poster.postErr = errors.New("temporary network error")
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world!", true); err == nil {
		t.Fatal("Deliver should surface the fallback post failure, not silently succeed")
	}
	if len(poster.calls) != 1 {
		t.Fatalf("PostToThread attempts = %d, want 1 (the failed attempt)", len(poster.calls))
	}
	if got := len(mgr.state); got != 1 {
		t.Fatalf("stream state entries = %d, want 1 (kept for retry, not forgotten on failure)", got)
	}

	poster.postErr = nil
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world!", true); err != nil {
		t.Fatalf("retry of the final chunk: %v", err)
	}
	if len(poster.calls) != 2 {
		t.Fatalf("PostToThread attempts = %d, want 2", len(poster.calls))
	}
	if got, want := poster.calls[1].Text, "Hello, world!"; got != want {
		t.Errorf("retried fallback post text = %q, want %q (not duplicated)", got, want)
	}
	if got := len(mgr.state); got != 0 {
		t.Errorf("stream state entries = %d, want 0 after the retry succeeds", got)
	}
}

func TestStreamManager_StopFailure_PreservesFinalChunkForRetryWithoutRestarting(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	streamer.stopErr = errors.New("temporary network error")
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, "!", true); err == nil {
		t.Fatal("Deliver should surface the stop failure, not silently succeed")
	}
	if len(streamer.stopCalls) != 1 {
		t.Fatalf("StopStream calls = %d, want 1 (the failed attempt)", len(streamer.stopCalls))
	}
	if got := len(mgr.state); got != 1 {
		t.Fatalf("stream state entries = %d, want 1 (kept for retry, not forgotten on failure)", got)
	}

	streamer.stopErr = nil
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, "!", true); err != nil {
		t.Fatalf("retry of the final chunk: %v", err)
	}
	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1 (the retry must not start a second stream)", len(streamer.startCalls))
	}
	if len(streamer.stopCalls) != 2 || streamer.stopCalls[1].text != "!" {
		t.Fatalf("StopStream calls = %+v, want a second call with '!'", streamer.stopCalls)
	}
	if got := len(mgr.state); got != 0 {
		t.Errorf("stream state entries = %d, want 0 after the retry succeeds", got)
	}
}

func TestStreamManager_ForgetsLiveStateAfterFinal(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := len(mgr.state); got != 0 {
		t.Errorf("stream state entries = %d, want 0 after final", got)
	}
}

// A message_id is unique within a session, so a repeat delivery to the same
// thread after it already finalized is always a duplicate.
func TestStreamManager_DuplicateAfterFinal_IsDroppedNotReposted(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver (duplicate): %v", err)
	}
	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1 (the duplicate must not start a second message)", len(streamer.startCalls))
	}
	if len(streamer.stopCalls) != 1 {
		t.Fatalf("StopStream calls = %d, want 1", len(streamer.stopCalls))
	}
}

// The same duplicate-drop applies to the fallback-post path: a workspace
// that can't stream still must show exactly one message per message_id.
func TestStreamManager_DuplicateAfterFallbackFinal_IsDroppedNotReposted(t *testing.T) {
	streamer := &recordingStreamer{startErr: errors.New("streaming not enabled for this app")}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", true); err != nil {
		t.Fatalf("Deliver (duplicate): %v", err)
	}
	if len(poster.calls) != 1 {
		t.Fatalf("PostToThread calls = %d, want 1 (the duplicate must not post a second message)", len(poster.calls))
	}
}

func TestStreamManager_IdenticalStreamKeysInDifferentThreadsRemainIndependent(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "first", false); err != nil {
		t.Fatalf("first thread's initial chunk: %v", err)
	}
	if err := mgr.Deliver("C1", "222.0", "msg-1", "", 0, "second", false); err != nil {
		t.Fatalf("second thread's initial chunk: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, " one", true); err != nil {
		t.Fatalf("first thread's final chunk: %v", err)
	}
	if err := mgr.Deliver("C1", "222.0", "msg-1", "", 1, " two", true); err != nil {
		t.Fatalf("second thread's final chunk: %v", err)
	}

	if got := len(streamer.startCalls); got != 2 {
		t.Fatalf("StartStream calls = %d, want 2", got)
	}
	if streamer.startCalls[0].threadTS != "111.0" || streamer.startCalls[0].text != "first" {
		t.Errorf("first StartStream call = %+v, want first thread's initial text", streamer.startCalls[0])
	}
	if streamer.startCalls[1].threadTS != "222.0" || streamer.startCalls[1].text != "second" {
		t.Errorf("second StartStream call = %+v, want second thread's initial text", streamer.startCalls[1])
	}
	if got := len(streamer.stopCalls); got != 2 {
		t.Fatalf("StopStream calls = %d, want 2", got)
	}
	if streamer.stopCalls[0].ts != "ts-1" || streamer.stopCalls[0].text != " one" {
		t.Errorf("first StopStream call = %+v, want first thread's final text", streamer.stopCalls[0])
	}
	if streamer.stopCalls[1].ts != "ts-2" || streamer.stopCalls[1].text != " two" {
		t.Errorf("second StopStream call = %+v, want second thread's final text", streamer.stopCalls[1])
	}
}

func TestStreamManager_RestartCompletesAndSuppressesTrailingMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streams.json")
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	beforeRestart := newRecipientStreamManagerAt(streamer, poster, path)

	if err := beforeRestart.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
		t.Fatalf("initial chunk: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		t.Fatalf("decode snapshot header: %v", err)
	}
	if header.Version != 1 {
		t.Fatalf("snapshot version = %d, want 1", header.Version)
	}

	afterRestart := newRecipientStreamManagerAt(streamer, poster, path)
	if err := afterRestart.Deliver("C1", "111.0", "msg-1", "", 1, ", world", true); err != nil {
		t.Fatalf("final chunk after restart: %v", err)
	}
	if err := afterRestart.Deliver("C1", "111.0", "msg-1", "", 0, "Hello, world", true); err != nil {
		t.Fatalf("trailing message after restart: %v", err)
	}

	if got := len(streamer.startCalls); got != 1 {
		t.Fatalf("StartStream calls = %d, want 1 (restart must resume the existing message)", got)
	}
	if got := len(streamer.stopCalls); got != 1 {
		t.Fatalf("StopStream calls = %d, want 1", got)
	}
	if got := streamer.stopCalls[0]; got.ts != "ts-1" || got.text != ", world" {
		t.Errorf("StopStream call = %+v, want existing stream ts with final text", got)
	}
	if got := len(poster.calls); got != 0 {
		t.Errorf("PostToThread calls = %d, want 0", got)
	}
}

func TestStreamManager_InvalidSnapshotStartsEmptyAndLogsOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{name: "missing", setup: func(t *testing.T, path string) {}},
		{name: "unreadable", setup: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatalf("create unreadable snapshot path: %v", err)
			}
		}},
		{name: "corrupt", setup: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatalf("write corrupt snapshot: %v", err)
			}
		}},
		{name: "wrong version", setup: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"version": 2}`), 0o600); err != nil {
				t.Fatalf("write wrong-version snapshot: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "streams.json")
			tc.setup(t, path)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			mgr := NewStreamManagerWithStatePath(&recordingStreamer{}, &recordingPoster{}, logger, path)

			if got := len(mgr.state); got != 0 {
				t.Errorf("live streams = %d, want 0", got)
			}
			if got := len(mgr.finalized); got != 0 {
				t.Errorf("finalized streams = %d, want 0", got)
			}
			if got := strings.Count(logs.String(), "starting empty"); got != 1 {
				t.Errorf("starting-empty logs = %d, want 1; logs=%s", got, logs.String())
			}
		})
	}
}

// Regression coverage for stateOrFinalized's atomicity: run with `-race`.
func TestStreamManager_ConcurrentDeliverToSameKey_PostsExactlyOnce(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := newRecipientStreamManager(streamer, poster)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := mgr.Deliver("C1", "111.0", "msg-race", "", 0, "Hello", true); err != nil {
				t.Errorf("Deliver: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := len(streamer.startCalls); got != 1 {
		t.Fatalf("StartStream calls = %d, want 1 (exactly one Slack message per message_id)", got)
	}
	if got := len(streamer.stopCalls); got != 1 {
		t.Fatalf("StopStream calls = %d, want 1", got)
	}
}

// newRecipientStreamManager returns a manager that already knows who each
// of the test threads' replies go to, so a test that is not about recipient
// selection still starts a native stream.
func newRecipientStreamManager(streamer Streamer, poster ThreadPoster) *StreamManager {
	return withTestRecipients(NewStreamManager(streamer, poster, testLogger()))
}

func newRecipientStreamManagerAt(streamer Streamer, poster ThreadPoster, path string) *StreamManager {
	return withTestRecipients(NewStreamManagerWithStatePath(streamer, poster, testLogger(), path))
}

func withTestRecipients(m *StreamManager) *StreamManager {
	m.RecordRecipient("C1", "111.0", "U1", "T1")
	m.RecordRecipient("C1", "222.0", "U1", "T1")
	return m
}

func newLoggedStreamManager(streamer Streamer, poster ThreadPoster) (*StreamManager, *bytes.Buffer) {
	var logs bytes.Buffer
	return NewStreamManager(streamer, poster, slog.New(slog.NewTextHandler(&logs, nil))), &logs
}

func TestStreamManager_RecipientIsKeyedByThread(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")
	mgr.RecordRecipient("C1", "222.0", "U-bob", "T-b")

	if err := mgr.Deliver("C1", "222.0", "msg-1", "", 0, "to bob", false); err != nil {
		t.Fatalf("second thread: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "to alice", false); err != nil {
		t.Fatalf("first thread: %v", err)
	}

	if len(streamer.startCalls) != 2 {
		t.Fatalf("StartStream calls = %d, want 2", len(streamer.startCalls))
	}
	if got := streamer.startCalls[0]; got.recipientUserID != "U-bob" || got.teamID != "T-b" {
		t.Errorf("thread 222.0 start = %+v, want U-bob/T-b", got)
	}
	if got := streamer.startCalls[1]; got.recipientUserID != "U-alice" || got.teamID != "T-a" {
		t.Errorf("thread 111.0 start = %+v, want U-alice/T-a", got)
	}
}

func TestStreamManager_RecipientIsNotSharedAcrossChannels(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")

	if err := mgr.Deliver("C2", "111.0", "msg-1", "", 0, "other channel", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if len(streamer.startCalls) != 0 {
		t.Errorf("StartStream calls = %+v, want none: C2's thread has no recipient of its own", streamer.startCalls)
	}
	if len(poster.calls) != 1 || poster.calls[0].Text != "other channel" {
		t.Errorf("PostToThread calls = %+v, want the one fallback post", poster.calls)
	}
}

func TestStreamManager_LaterSpeakerIsRecipientOfNextStreamOnly(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")

	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "first", false); err != nil {
		t.Fatalf("first turn start: %v", err)
	}
	mgr.RecordRecipient("C1", "111.0", "U-bob", "T-a")
	if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, " turn", true); err != nil {
		t.Fatalf("first turn final: %v", err)
	}
	if err := mgr.Deliver("C1", "111.0", "msg-2", "", 0, "second", true); err != nil {
		t.Fatalf("second turn: %v", err)
	}

	if len(streamer.startCalls) != 2 {
		t.Fatalf("StartStream calls = %d, want 2 (a new speaker must not restart a running stream)", len(streamer.startCalls))
	}
	if got := streamer.startCalls[0].recipientUserID; got != "U-alice" {
		t.Errorf("first stream recipient = %q, want U-alice", got)
	}
	if got := streamer.startCalls[1].recipientUserID; got != "U-bob" {
		t.Errorf("second stream recipient = %q, want U-bob (the latest speaker)", got)
	}
}

func TestStreamManager_RecipientSurvivesRestartBeforeStreamStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streams.json")
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	beforeRestart := NewStreamManagerWithStatePath(streamer, poster, testLogger(), path)
	beforeRestart.RecordRecipient("C1", "111.0", "U-alice", "T-a")

	if err := beforeRestart.Deliver("C1", "111.0", "msg-1", "", 1, "world", false); err != nil {
		t.Fatalf("buffered chunk: %v", err)
	}
	afterRestart := NewStreamManagerWithStatePath(streamer, poster, testLogger(), path)
	if err := afterRestart.Deliver("C1", "111.0", "msg-1", "", 0, "Hello ", false); err != nil {
		t.Fatalf("gap-closing chunk: %v", err)
	}

	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1", len(streamer.startCalls))
	}
	if got := streamer.startCalls[0]; got.recipientUserID != "U-alice" || got.teamID != "T-a" {
		t.Errorf("start after restart = %+v, want the recipient captured before the restart", got)
	}
}

func TestStreamManager_UnresolvedRecipient_FallsBackToOnePostAndLogsReason(t *testing.T) {
	for _, tc := range []struct {
		name       string
		userID     string
		teamID     string
		wantReason string
	}{
		{name: "no recipient recorded", wantReason: "recipient_unknown"},
		{name: "recipient without team", userID: "U-alice", wantReason: "recipient_team_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streamer := &recordingStreamer{}
			poster := &recordingPoster{}
			mgr, logs := newLoggedStreamManager(streamer, poster)
			if tc.userID != "" {
				mgr.RecordRecipient("C1", "111.0", tc.userID, tc.teamID)
			}

			for i, text := range []string{"SECRET-BODY-1 ", "SECRET-BODY-2"} {
				if err := mgr.Deliver("C1", "111.0", "msg-1", "", int64(i), text, i == 1); err != nil {
					t.Fatalf("chunk %d: %v", i, err)
				}
			}

			if len(streamer.startCalls) != 0 {
				t.Errorf("StartStream calls = %+v, want none without a complete recipient", streamer.startCalls)
			}
			if len(poster.calls) != 1 {
				t.Fatalf("PostToThread calls = %d, want exactly 1", len(poster.calls))
			}
			out := logs.String()
			if got := strings.Count(out, "event=stream_start_skipped"); got != 1 {
				t.Fatalf("stream_start_skipped logs = %d, want 1; logs=%s", got, out)
			}
			for _, want := range []string{"reason=" + tc.wantReason, "thread_ts=111.0", "stream_key=msg-1"} {
				if !strings.Contains(out, want) {
					t.Errorf("log missing %q; logs=%s", want, out)
				}
			}
			if strings.Contains(out, "SECRET-BODY") {
				t.Errorf("log must not contain message text; logs=%s", out)
			}
		})
	}
}

func TestStreamManager_StartFailure_LogsReasonOnceWithoutBodyAndPostsOnce(t *testing.T) {
	streamer := &recordingStreamer{startErr: errors.New("channel_not_found")}
	poster := &recordingPoster{}
	mgr, logs := newLoggedStreamManager(streamer, poster)
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")

	for i, text := range []string{"SECRET-BODY-1 ", "SECRET-BODY-2 ", "SECRET-BODY-3"} {
		if err := mgr.Deliver("C1", "111.0", "msg-1", "", int64(i), text, i == 2); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}

	if len(streamer.startCalls) != 1 {
		t.Errorf("StartStream calls = %d, want 1 (no retry per chunk)", len(streamer.startCalls))
	}
	if len(poster.calls) != 1 {
		t.Fatalf("PostToThread calls = %d, want exactly 1", len(poster.calls))
	}
	out := logs.String()
	if got := strings.Count(out, "event=stream_start_failed"); got != 1 {
		t.Fatalf("stream_start_failed logs = %d, want 1; logs=%s", got, out)
	}
	for _, want := range []string{"thread_ts=111.0", "stream_key=msg-1", "channel_not_found"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q; logs=%s", want, out)
		}
	}
	if strings.Contains(out, "SECRET-BODY") {
		t.Errorf("log must not contain message text; logs=%s", out)
	}
}

func TestStreamManager_MidStreamFailure_NeverPostsAlongsideNativeStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(s *recordingStreamer)
	}{
		{name: "append", fail: func(s *recordingStreamer) { s.appendErr = errors.New("ratelimited") }},
		{name: "stop", fail: func(s *recordingStreamer) { s.stopErr = errors.New("ratelimited") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streamer := &recordingStreamer{}
			poster := &recordingPoster{}
			mgr := newRecipientStreamManager(streamer, poster)

			if err := mgr.Deliver("C1", "111.0", "msg-1", "", 0, "Hello", false); err != nil {
				t.Fatalf("start: %v", err)
			}
			tc.fail(streamer)
			if err := mgr.Deliver("C1", "111.0", "msg-1", "", 1, ", world", false); tc.name == "append" && err == nil {
				t.Fatal("append failure must be returned to the caller")
			}
			if err := mgr.Deliver("C1", "111.0", "msg-1", "", 2, "!", true); err == nil {
				t.Fatal("a failed stream must keep reporting the failure so the caller retries")
			}

			if len(poster.calls) != 0 {
				t.Errorf("PostToThread calls = %+v, want none: a native message already exists", poster.calls)
			}
			if len(streamer.startCalls) != 1 {
				t.Errorf("StartStream calls = %d, want 1: a retry must not open a second stream", len(streamer.startCalls))
			}
		})
	}
}

func TestStreamManager_LaterMessageBeforeFirstChunkDoesNotTakeOverEarlierReply(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")
	mgr.RecordRecipient("C1", "111.0", "U-bob", "T-a")

	for _, key := range []string{"msg-1", "msg-2", "msg-3"} {
		if err := mgr.Deliver("C1", "111.0", key, "", 0, "reply", true); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}

	var got []string
	for _, c := range streamer.startCalls {
		got = append(got, c.recipientUserID)
	}
	if want := []string{"U-alice", "U-bob", "U-bob"}; !slices.Equal(got, want) {
		t.Errorf("recipients of the three replies = %v, want %v: each reply answers the oldest unanswered sender, and a reply with nobody pending keeps the last one", got, want)
	}
}

func TestStreamManager_MessagesOfOneTurnShareTheTurnsRecipient(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")
	mgr.RecordRecipient("C1", "111.0", "U-bob", "T-a")

	for _, d := range []struct{ key, turn string }{{"msg-1", "turn-1"}, {"msg-2", "turn-1"}, {"msg-3", "turn-2"}} {
		if err := mgr.Deliver("C1", "111.0", d.key, d.turn, 0, "reply", true); err != nil {
			t.Fatalf("%s: %v", d.key, err)
		}
	}

	var got []string
	for _, c := range streamer.startCalls {
		got = append(got, c.recipientUserID)
	}
	if want := []string{"U-alice", "U-alice", "U-bob"}; !slices.Equal(got, want) {
		t.Errorf("recipients = %v, want %v: an interim message must not consume the next sender's turn", got, want)
	}
}

func TestStreamManager_RepeatedMessagesFromOneSenderCountAsOnePendingTurn(t *testing.T) {
	streamer := &recordingStreamer{}
	poster := &recordingPoster{}
	mgr := NewStreamManager(streamer, poster, testLogger())
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")
	mgr.RecordRecipient("C1", "111.0", "U-alice", "T-a")
	mgr.RecordRecipient("C1", "111.0", "U-bob", "T-a")

	for _, key := range []string{"msg-1", "msg-2"} {
		if err := mgr.Deliver("C1", "111.0", key, "", 0, "reply", true); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}

	if got := streamer.startCalls[1].recipientUserID; got != "U-bob" {
		t.Errorf("second reply recipient = %q, want U-bob: an agent may answer a sender's burst in one turn", got)
	}
}
