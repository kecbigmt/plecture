package eventlog

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/contracts/event"
)

// TestMain lets a test re-exec itself as a child appender so we can exercise the
// cross-process flock path (the in-process mutex hides it within one Store).
func TestMain(m *testing.M) {
	if dir := os.Getenv("EVENTLOG_CHILD_DIR"); dir != "" {
		session := os.Getenv("EVENTLOG_CHILD_SESSION")
		n, _ := strconv.Atoi(os.Getenv("EVENTLOG_CHILD_N"))
		s := NewStore(dir)
		for range n {
			if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "test.tick", Source: "test"}); err != nil {
				os.Exit(1)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTombstoneRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())

	if _, ok, err := s.ReadTombstone("o/r-1"); err != nil || ok {
		t.Fatalf("expected no tombstone yet, got ok=%v err=%v", ok, err)
	}

	want := []byte(`{"session_name":"o/r-1","destroyed_at":"2026-07-05T00:00:00Z"}`)
	if err := s.WriteTombstone("o/r-1", want); err != nil {
		t.Fatalf("WriteTombstone: %v", err)
	}

	got, ok, err := s.ReadTombstone("o/r-1")
	if err != nil {
		t.Fatalf("ReadTombstone: %v", err)
	}
	if !ok {
		t.Fatal("expected tombstone to exist")
	}
	if string(got) != string(want) {
		t.Errorf("ReadTombstone = %s, want %s", got, want)
	}

	// A later destroy overwrites, it doesn't append.
	overwrite := []byte(`{"session_name":"o/r-1","destroyed_at":"2026-07-06T00:00:00Z"}`)
	if err := s.WriteTombstone("o/r-1", overwrite); err != nil {
		t.Fatalf("WriteTombstone (overwrite): %v", err)
	}
	got, _, _ = s.ReadTombstone("o/r-1")
	if string(got) != string(overwrite) {
		t.Errorf("ReadTombstone after overwrite = %s, want %s", got, overwrite)
	}
}

func TestAppendAndList(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "octocat/hello-world-42"

	want := []struct{ typ, src string }{
		{"github.ci_status", event.SourceGitHub},
		{"slack.message", event.SourceSlack},
		{"claude.reply", event.SourceClaude},
	}
	var offsets []int64
	for _, w := range want {
		_, off, next, err := s.Append(event.Event{SessionName: session, Type: w.typ, Source: w.src})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if len(offsets) > 0 && off <= offsets[len(offsets)-1] {
			t.Fatalf("offset not increasing: %d after %d", off, offsets[len(offsets)-1])
		}
		offsets = append(offsets, off)
		_ = next
	}

	// directory name must be the escaped opaque session, not owner/repo split
	if _, err := os.Stat(filepath.Join(s.root, "octocat%2Fhello-world-42", "log.jsonl")); err != nil {
		t.Fatalf("expected escaped session dir: %v", err)
	}

	all, offs, _, err := s.List(session, 0, event.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d events, want 3", len(all))
	}
	for i := range all {
		if all[i].ID == "" || all[i].Time.IsZero() {
			t.Errorf("event %d missing id/time", i)
		}
		if offs[i] != offsets[i] {
			t.Errorf("list offset[%d]=%d, append said %d", i, offs[i], offsets[i])
		}
	}

	// type glob filter
	gh, _, _, err := s.List(session, 0, event.Filter{Types: []string{"github.*"}})
	if err != nil || len(gh) != 1 || gh[0].Type != "github.ci_status" {
		t.Fatalf("github.* filter: %v len=%d", err, len(gh))
	}

	// since-offset resumes after the first record
	rest, _, _, err := s.List(session, offsets[1], event.Filter{})
	if err != nil || len(rest) != 2 {
		t.Fatalf("since filter: %v len=%d", err, len(rest))
	}
}

func TestListFiltersByStreamID(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "owner/repo-3"

	// Two events tagged with stream "a", one with "b", one untagged.
	appends := []event.Event{
		{SessionName: session, Type: "claude.reply", StreamID: "a", Summary: "a1"},
		{SessionName: session, Type: "slack.message", StreamID: "b", Summary: "b1"},
		{SessionName: session, Type: "claude.reply", StreamID: "a", Summary: "a2"},
		{SessionName: session, Type: "user.note", Summary: "none"},
	}
	for _, ev := range appends {
		if _, _, _, err := s.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// stream_id filter returns only matching events, in append order.
	got, _, _, err := s.List(session, 0, event.Filter{StreamID: "a"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Summary != "a1" || got[1].Summary != "a2" {
		t.Fatalf("stream a filter = %v", summaries(got))
	}

	// The persisted StreamID survives the JSON round trip through the log.
	if got[0].StreamID != "a" {
		t.Errorf("stream_id not persisted: got %q", got[0].StreamID)
	}

	// Empty filter is unchanged: all four events.
	all, _, _, err := s.List(session, 0, event.Filter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("empty filter = %d events (err=%v), want 4", len(all), err)
	}
}

func TestSessionsEnumeratesLogDirs(t *testing.T) {
	s := NewStore(t.TempDir())

	// No root yet → empty, no error.
	if names, err := s.Sessions(); err != nil || len(names) != 0 {
		t.Fatalf("empty store: names=%v err=%v", names, err)
	}

	for _, name := range []string{"octocat/hello-world-42", "owner/repo-1", "owner/repo-1+tag"} {
		if _, _, _, err := s.Append(event.Event{SessionName: name, Type: "t"}); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	got, err := s.Sessions()
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	// Sorted, and the opaque names are decoded back from their escaped dir names.
	want := []string{"octocat/hello-world-42", "owner/repo-1", "owner/repo-1+tag"}
	if len(got) != len(want) {
		t.Fatalf("sessions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sessions[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListStreamMergesAcrossSessions(t *testing.T) {
	s := NewStore(t.TempDir())

	// Two sessions in stream "a", one in stream "b", one untagged. Interleave the
	// appends so chronological (ULID) order spans sessions.
	seq := []struct{ session, stream, summary string }{
		{"o/r-1", "a", "a1"},
		{"o/r-2", "b", "b1"},
		{"o/r-3", "a", "a2"},  // different session, same stream as a1
		{"o/r-1", "", "none"}, // untagged
		{"o/r-2", "a", "a3"},  // session that also has a "b" event
	}
	for _, e := range seq {
		if _, _, _, err := s.Append(event.Event{SessionName: e.session, StreamID: e.stream, Type: "t", Summary: e.summary}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := s.ListStream("a", event.Filter{})
	if err != nil {
		t.Fatalf("liststream: %v", err)
	}
	// Stream "a" spans o/r-1, o/r-3, o/r-2, returned in append (ULID) order as
	// one timeline; "b" and the untagged event must not appear.
	want := []string{"a1", "a2", "a3"}
	if len(got) != len(want) {
		t.Fatalf("stream a = %v, want %v", summaries(got), want)
	}
	for i := range want {
		if got[i].Summary != want[i] {
			t.Fatalf("stream a[%d] = %q, want %q", i, got[i].Summary, want[i])
		}
		if got[i].StreamID != "a" {
			t.Fatalf("event %d not in stream a: %q", i, got[i].StreamID)
		}
	}

	// ids must be ascending (the cross-session keyset relies on it).
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Fatalf("ids not ascending across sessions: %q then %q", got[i-1].ID, got[i].ID)
		}
	}

	// An empty stream id is rejected (no accidental "all sessions" sweep).
	if _, err := s.ListStream("", event.Filter{}); err == nil {
		t.Fatalf("expected error for empty stream id")
	}
}

// ListAcross is the merge primitive behind both cross-session views: it merges
// the named sessions' events in id order regardless of stream id, applying the
// filter's other selectors. Sessions not named are excluded.
func TestListAcrossMergesNamedSessions(t *testing.T) {
	s := NewStore(t.TempDir())

	seq := []struct{ session, summary string }{
		{"root", "r1"}, {"work", "w1"}, {"outside", "x1"}, {"work", "w2"}, {"root", "r2"},
	}
	for _, e := range seq {
		if _, _, _, err := s.Append(event.Event{SessionName: e.session, Type: "t", Source: "test", Summary: e.summary}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := s.ListAcross([]string{"root", "work"}, event.Filter{})
	if err != nil {
		t.Fatalf("listacross: %v", err)
	}
	want := []string{"r1", "w1", "w2", "r2"}
	if len(got) != len(want) {
		t.Fatalf("across = %v, want %v", summaries(got), want)
	}
	for i := range want {
		if got[i].Summary != want[i] {
			t.Fatalf("across[%d] = %q, want %q", i, got[i].Summary, want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Fatalf("ids not ascending: %q then %q", got[i-1].ID, got[i].ID)
		}
	}

	// An empty name set yields no events, not an error (an empty subtree is valid).
	if evs, err := s.ListAcross(nil, event.Filter{}); err != nil || len(evs) != 0 {
		t.Fatalf("empty name set = (%v, %v), want (nil, nil)", summaries(evs), err)
	}
}

func TestFollowStreamPicksUpNewSessions(t *testing.T) {
	s := NewStore(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan event.Event, 8)
	go func() { _ = s.FollowStream(ctx, "a", event.Filter{}, func(ev event.Event) { got <- ev }) }()

	time.Sleep(50 * time.Millisecond)
	// First event in an existing session…
	if _, _, _, err := s.Append(event.Event{SessionName: "o/r-1", StreamID: "a", Type: "t", Summary: "first"}); err != nil {
		t.Fatal(err)
	}
	// …then a brand-new session joins the same stream and must be picked up.
	if _, _, _, err := s.Append(event.Event{SessionName: "o/r-2", StreamID: "a", Type: "t", Summary: "second"}); err != nil {
		t.Fatal(err)
	}
	// An event in a different stream must never be delivered.
	if _, _, _, err := s.Append(event.Event{SessionName: "o/r-3", StreamID: "b", Type: "t", Summary: "other"}); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{"first": true, "second": true}
	deadline := time.After(3 * time.Second)
	for len(want) > 0 {
		select {
		case ev := <-got:
			if ev.Summary == "other" {
				t.Fatalf("event from a different stream leaked into the follow")
			}
			delete(want, ev.Summary)
		case <-deadline:
			t.Fatalf("FollowStream did not deliver: still waiting for %v", want)
		}
	}
}

func TestListDropsTornTrailingLine(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "o/r-1"
	_, _, next, err := s.Append(event.Event{SessionName: session, Type: "a"})
	if err != nil {
		t.Fatal(err)
	}
	// simulate an in-flight append: a partial line with no trailing newline
	f, err := os.OpenFile(s.logPath(session), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"x","session_name":"o/r-1","type":"b"`) // no "}\n"
	f.Close()

	evs, _, gotNext, err := s.List(session, 0, event.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || evs[0].Type != "a" {
		t.Fatalf("expected only the complete record, got %d", len(evs))
	}
	if gotNext != next {
		t.Fatalf("next=%d, want %d (partial line not counted)", gotNext, next)
	}
}

func TestListSkipsMalformedLineButAdvancesCursorPastIt(t *testing.T) {
	s := NewStore(t.TempDir())
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))

	const session = "o/r-2"
	if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "a"}); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(s.logPath(session), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not valid json}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, _, next, err := s.Append(event.Event{SessionName: session, Type: "b"})
	if err != nil {
		t.Fatal(err)
	}

	evs, _, gotNext, err := s.List(session, 0, event.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 2 || evs[0].Type != "a" || evs[1].Type != "b" {
		t.Fatalf("expected the two well-formed records, got %+v", evs)
	}
	if gotNext != next {
		t.Fatalf("next=%d, want %d: cursor must advance past the malformed line, or every later event wedges behind it", gotNext, next)
	}
	if !strings.Contains(logs.String(), "malformed") || !strings.Contains(logs.String(), session) {
		t.Fatalf("expected a malformed-record warning naming the session, got %q", logs.String())
	}
}

func TestConcurrentAppendMultiProcess(t *testing.T) {
	dir := t.TempDir()
	const session = "owner/repo-7+tag"
	const procs, per = 5, 20

	var cmds []*exec.Cmd
	for i := range procs {
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(),
			"EVENTLOG_CHILD_DIR="+dir,
			"EVENTLOG_CHILD_SESSION="+session,
			"EVENTLOG_CHILD_N="+strconv.Itoa(per),
		)
		if err := c.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
		cmds = append(cmds, c)
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("child %d failed: %v", i, err)
		}
	}

	s := NewStore(dir)
	evs, offs, _, err := s.List(session, 0, event.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != procs*per {
		t.Fatalf("got %d events, want %d (lost/torn appends?)", len(evs), procs*per)
	}
	ids := map[string]bool{}
	var prev int64 = -1
	for i := range evs {
		if evs[i].ID == "" || ids[evs[i].ID] {
			t.Fatalf("duplicate/empty id at %d: %q", i, evs[i].ID)
		}
		ids[evs[i].ID] = true
		if offs[i] <= prev {
			t.Fatalf("offset not strictly increasing at %d: %d after %d", i, offs[i], prev)
		}
		prev = offs[i]
	}
}

func TestTailReturnsLastN(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "owner/repo-9"
	for i := range 25 {
		if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "t", Summary: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	// limit < total: the last N in append order.
	got, err := s.Tail(session, event.Filter{}, 10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(got) != 10 || got[0].Summary != "15" || got[9].Summary != "24" {
		t.Fatalf("tail(10) = %d events, first=%q last=%q", len(got), summaryAt(got, 0), summaryAt(got, len(got)-1))
	}

	// limit > total: everything.
	all, err := s.Tail(session, event.Filter{}, 100)
	if err != nil || len(all) != 25 {
		t.Fatalf("tail(100) = %d events (err=%v), want 25", len(all), err)
	}

	// limit <= 0: everything.
	zero, err := s.Tail(session, event.Filter{}, 0)
	if err != nil || len(zero) != 25 {
		t.Fatalf("tail(0) = %d events, want all 25", len(zero))
	}

	// missing session: empty, no error.
	none, err := s.Tail("never/created-1", event.Filter{}, 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("tail(missing) = %d events (err=%v)", len(none), err)
	}
}

func TestTailOffset(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "owner/repo-9"
	for i := range 25 {
		if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "t", Summary: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	// n < total: the offset replays exactly the last n records (15..24).
	off, err := s.TailOffset(session, event.Filter{}, 10)
	if err != nil {
		t.Fatalf("tailOffset: %v", err)
	}
	evs, _, _, err := s.List(session, off, event.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 10 || evs[0].Summary != "15" || evs[9].Summary != "24" {
		t.Fatalf("from tail offset got %d events, first=%q last=%q", len(evs), summaryAt(evs, 0), summaryAt(evs, len(evs)-1))
	}

	// n >= total and n <= 0: replay from the head (offset 0).
	if off, _ := s.TailOffset(session, event.Filter{}, 100); off != 0 {
		t.Errorf("tailOffset(100) = %d, want 0 (replay all)", off)
	}
	if off, _ := s.TailOffset(session, event.Filter{}, 0); off != 0 {
		t.Errorf("tailOffset(0) = %d, want 0", off)
	}

	// missing session: 0, no error.
	if off, err := s.TailOffset("never/created-1", event.Filter{}, 5); err != nil || off != 0 {
		t.Errorf("tailOffset(missing) = %d (err=%v)", off, err)
	}
}

func TestTailAppliesFilterToTheRing(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "owner/repo-9"
	// Interleave two types so the newest few records are NOT all "keep".
	for i := range 20 {
		typ := "skip"
		if i%2 == 0 {
			typ = "keep"
		}
		if _, _, _, err := s.Append(event.Event{SessionName: session, Type: typ, Summary: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	// The ring must keep the last N *matching* events, not the last N then
	// filter (which would drop most of them). "keep" is on even i: 0,2,…,18.
	got, err := s.Tail(session, event.Filter{Types: []string{"keep"}}, 3)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(got) != 3 || got[0].Summary != "14" || got[2].Summary != "18" {
		t.Fatalf("filtered tail(3) = %v", summaries(got))
	}
}

func TestGen(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "owner/repo-9"

	// No log yet → no generation, no error.
	if g, err := s.Gen(session); err != nil || g != "" {
		t.Fatalf("gen of empty log = %q (err=%v), want empty", g, err)
	}

	if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "a"}); err != nil {
		t.Fatal(err)
	}
	g1, err := s.Gen(session)
	if err != nil || g1 == "" {
		t.Fatalf("gen after first append = %q (err=%v), want non-empty", g1, err)
	}

	// Stable across further appends (no rotation).
	if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "b"}); err != nil {
		t.Fatal(err)
	}
	g2, err := s.Gen(session)
	if err != nil || g2 != g1 {
		t.Fatalf("gen changed across appends: %q → %q", g1, g2)
	}
}

func summaries(evs []event.Event) []string {
	out := make([]string, len(evs))
	for i := range evs {
		out[i] = evs[i].Summary
	}
	return out
}

func summaryAt(evs []event.Event, i int) string {
	if i < 0 || i >= len(evs) {
		return ""
	}
	return evs[i].Summary
}

func TestCursorRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	const session, consumer = "o/r-1", "claude"
	if off, err := s.ReadCursor(session, consumer); err != nil || off != 0 {
		t.Fatalf("missing cursor should be 0: off=%d err=%v", off, err)
	}
	if err := s.CommitCursor(session, consumer, 4096); err != nil {
		t.Fatalf("commit: %v", err)
	}
	off, err := s.ReadCursor(session, consumer)
	if err != nil || off != 4096 {
		t.Fatalf("read cursor: off=%d err=%v", off, err)
	}
}

func TestHasCursor(t *testing.T) {
	s := NewStore(t.TempDir())
	const session, consumer = "o/r-1", "dispatcher"
	if s.HasCursor(session, consumer) {
		t.Error("HasCursor true before any commit")
	}
	// A committed 0 must read as "has a cursor" (distinct from never-committed).
	if err := s.CommitCursor(session, consumer, 0); err != nil {
		t.Fatal(err)
	}
	if !s.HasCursor(session, consumer) {
		t.Error("HasCursor false after committing 0")
	}
}

func TestFollowDeliversNewEvents(t *testing.T) {
	s := NewStore(t.TempDir())
	const session = "o/r-1"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan event.Event, 4)
	go func() { _ = s.Follow(ctx, session, 0, func(ev event.Event, _ int64) { got <- ev }) }()

	time.Sleep(50 * time.Millisecond)
	if _, _, _, err := s.Append(event.Event{SessionName: session, Type: "live"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-got:
		if ev.Type != "live" {
			t.Fatalf("got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Follow did not deliver the appended event")
	}
}
