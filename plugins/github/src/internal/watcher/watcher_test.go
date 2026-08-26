package watcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/contracts/event"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/flocktest"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/ratebudget"
)

func TestStore_SubscribeUnsubscribe(t *testing.T) {
	store := NewStore(t.TempDir())
	const res1 = "https://github.com/org/repo/issues/1"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: res1, Branch: "issue/1"}); err != nil {
		t.Fatal(err)
	}
	subs, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[subKey("org/repo-1", res1)].Branch != "issue/1" {
		t.Fatalf("subs = %+v", subs)
	}

	// Re-subscribe keeps the baseline (idempotent task retry).
	if err := store.SetLast("org/repo-1", res1, map[string]string{"pr_state": "open"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: res1}); err != nil {
		t.Fatal(err)
	}
	subs, _ = store.All()
	if subs[subKey("org/repo-1", res1)].Last["pr_state"] != "open" {
		t.Error("re-subscribe must keep the observed baseline")
	}

	if err := store.Unsubscribe("org/repo-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Unsubscribe("org/repo-1"); err != nil {
		t.Fatal("unsubscribe must be idempotent:", err)
	}
	subs, _ = store.All()
	if len(subs) != 0 {
		t.Fatalf("subs after unsubscribe = %+v", subs)
	}
}

// The Linux NFS client rejects LOCK_EX on an O_RDONLY descriptor with EBADF,
// even though local filesystems tolerate it. This test inspects the lock
// file descriptor's own open flags via /proc, so it catches the regression
// even on a local (non-NFS) test filesystem.
func TestStore_UpdateOpensLockFileWritable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lock fd flags are inspected via /proc, which is Linux-specific")
	}

	store := NewStore(t.TempDir())
	lockPath := store.path + ".lock"

	err := store.update(func(*registry) error {
		accMode, err := flocktest.AccessMode(lockPath)
		if err != nil {
			return err
		}
		if accMode == os.O_RDONLY {
			return fmt.Errorf("lock file opened O_RDONLY; exclusive lock (LOCK_EX) requires a writable descriptor on NFS")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

// A re-subscribe with an empty Branch must NOT wipe a value a prior
// subscribe stored: runtime `plect subscribe` omits --branch, and re-subscribing
// a resource a dispatch-time auto-subscribe already recorded (with branch) must
// keep that branch — else an issue session loses its linked-PR resolution
// A non-empty incoming field still updates.
func TestStore_ResubscribePreservesNonEmptyFields(t *testing.T) {
	store := NewStore(t.TempDir())
	const res = "https://github.com/org/repo/issues/1"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: res, Branch: "issue/1"}); err != nil {
		t.Fatal(err)
	}
	// Runtime re-subscribe of the same resource: no branch.
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: res}); err != nil {
		t.Fatal(err)
	}
	subs, _ := store.All()
	got := subs[subKey("org/repo-1", res)]
	if got.Branch != "issue/1" {
		t.Errorf("Branch = %q, want preserved issue/1", got.Branch)
	}
	// A non-empty incoming value is authoritative and updates.
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: res, Branch: "issue/1+rework"}); err != nil {
		t.Fatal(err)
	}
	subs, _ = store.All()
	if subs[subKey("org/repo-1", res)].Branch != "issue/1+rework" {
		t.Errorf("Branch = %q, want updated issue/1+rework", subs[subKey("org/repo-1", res)].Branch)
	}
}

// One session may funnel several resources (N:1): each (session, resource) is a
// distinct subscription with its own baseline, and --resource removes just one.
func TestStore_MultipleResourcesPerSession(t *testing.T) {
	store := NewStore(t.TempDir())
	const pr1 = "https://github.com/org/repo/pull/1"
	const pr2 = "https://github.com/org/repo/pull/2"
	for _, r := range []string{pr1, pr2} {
		if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: r}); err != nil {
			t.Fatal(err)
		}
	}
	subs, _ := store.All()
	if len(subs) != 2 {
		t.Fatalf("want 2 subscriptions for one session, got %+v", subs)
	}

	// Per-resource baselines are independent.
	if err := store.SetLast("org/repo-1", pr1, map[string]string{"pr_state": "merged"}); err != nil {
		t.Fatal(err)
	}
	subs, _ = store.All()
	if subs[subKey("org/repo-1", pr1)].Last["pr_state"] != "merged" {
		t.Error("pr1 baseline not set")
	}
	if subs[subKey("org/repo-1", pr2)].Last["pr_state"] != "" {
		t.Error("pr2 baseline must be independent of pr1")
	}

	// --resource removes only that one; the other survives.
	if err := store.UnsubscribeResource("org/repo-1", pr1); err != nil {
		t.Fatal(err)
	}
	subs, _ = store.All()
	if len(subs) != 1 || subs[subKey("org/repo-1", pr2)] == nil {
		t.Fatalf("only pr1 should be removed, got %+v", subs)
	}

	// Bare unsubscribe clears the rest.
	if err := store.Unsubscribe("org/repo-1"); err != nil {
		t.Fatal(err)
	}
	subs, _ = store.All()
	if len(subs) != 0 {
		t.Fatalf("session unsubscribe must clear all resources, got %+v", subs)
	}
}

// A subscriptions file from the pre-N:1 format (version 1) is discarded on load,
// not migrated — `plect subscribe` re-creates it.
func TestStore_DiscardsOldFormat(t *testing.T) {
	dir := t.TempDir()
	// version 1, keyed by session name (the old shape).
	old := `{"version":1,"subscriptions":{"org/repo-1":{"session_name":"org/repo-1","resource":"https://github.com/org/repo/pull/1","last":{"pr_state":"open"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "subscriptions.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	subs, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Fatalf("old-format file must be discarded, got %+v", subs)
	}
}

// fakeBin writes an executable shell stub into dir and returns its path.
func fakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ghRoute answers one `gh` invocation whose full argument list contains match.
// Routes are tried in order; the first match wins. fail makes the stub exit
// non-zero (simulating a network/API error, not an HTTP error status —
// poll.go's -i/status-line parsing never sees this case) instead of printing
// stdout.
type ghRoute struct {
	match  string
	stdout string
	fail   bool
}

// fakeGh writes a `gh` stub that answers the first matching route by
// wrapping stdout as a synthetic `gh api -i` HTTP/1.1 200 response (poll.go
// parses the status line + headers unconditionally) — callers only supply
// the body, matching the shape tests used before conditional GET support was
// added. Each test wires up only the routes its scenario needs; anything
// unmatched prints a bare empty 200 (an empty, not a failed, response). When
// log is non-empty, every invocation's arguments are appended to that file
// (one line each) for call-count assertions.
func fakeGh(t *testing.T, dir, log string, routes []ghRoute) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	if log != "" {
		b.WriteString("echo \"$@\" >> " + log + "\n")
	}
	b.WriteString("args=\"$*\"\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "if [[ \"$args\" == *'%s'* ]]; then\n", r.match)
		if r.fail {
			b.WriteString("  printf 'HTTP/1.1 500 Internal Server Error\\n\\n'\n  exit 1\n")
		} else {
			b.WriteString("  printf 'HTTP/1.1 200 OK\\n\\n'\n  cat <<'GHSTUB_EOF'\n" + r.stdout + "\nGHSTUB_EOF\n  exit 0\n")
		}
		b.WriteString("fi\n")
	}
	b.WriteString("printf 'HTTP/1.1 200 OK\\n\\n'\n")
	return fakeBin(t, dir, "gh", b.String())
}

// prCoreRoute answers the primary REST PR probe (state/merged/head sha) with
// mergeable_state/draft omitted — safe for tests that don't exercise those
// fields (mergeable stays unresolved, draft stays false without ever firing
// a notification since old never observes "true").
func prCoreRoute(number, state string, merged bool, sha string) ghRoute {
	return prCoreRouteFull(number, state, merged, sha, "", false)
}

// prCoreRouteFull is prCoreRoute plus explicit mergeable_state/draft, for
// tests exercising the github.mergeable/github.draft events.
func prCoreRouteFull(number, state string, merged bool, sha, mergeableState string, draft bool) ghRoute {
	return ghRoute{
		match:  fmt.Sprintf("pulls/%s ", number),
		stdout: fmt.Sprintf(`{"state":%q,"merged":%v,"sha":%q,"mergeable_state":%q,"draft":%v}`, state, merged, sha, mergeableState, draft),
	}
}

// issueCoreRoute answers the REST issue probe (state only).
func issueCoreRoute(number, state string) ghRoute {
	return ghRoute{
		match:  fmt.Sprintf("issues/%s ", number),
		stdout: fmt.Sprintf(`{"state":%q}`, state),
	}
}

// linkedPRDiscoveryRoute answers the `pulls?head=` list probe with a bare PR
// number (empty string = no PR found). The match ends in a space so it never
// collides with a specific pulls/{n} probe (which has "/n" — no space —
// right after "pulls").
func linkedPRDiscoveryRoute(number string) ghRoute {
	return ghRoute{match: "pulls ", stdout: number}
}

func TestPoller_TickNotifiesAndAdvancesBaseline(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	resource := "https://github.com/org/repo/pull/7"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7", Resource: resource}); err != nil {
		t.Fatal(err)
	}
	// Baseline exists so the transition produces notifications.
	if err := store.SetLast("org/repo-7", resource, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean", "draft": "false",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"conclusion":"success"}`},
		{match: "/status", stdout: ""},
		prCoreRouteFull("7", "closed", true, "deadbeef1", "dirty", false),
	})
	plectLog := filepath.Join(t.TempDir(), "plect.log")
	fakeBin(t, binDir, "plect", `echo "$@" >> `+plectLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := &Poller{
		Store:     store,
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		NotifyURL: srv.URL,
		GhBin:     gh,
		Guard:     ratebudget.NewGuard(t.TempDir()),
	}
	p.Tick()

	if _, err := os.Stat(plectLog); !os.IsNotExist(err) {
		t.Fatalf("poll must not invoke plect state set-output, stat err=%v", err)
	}

	// state + ci_status + mergeable transitions → notifications.
	if len(notified) != 3 {
		t.Fatalf("expected 3 notifications, got %d: %+v", len(notified), notified)
	}
	types := map[string]bool{}
	for _, n := range notified {
		types[n["change_type"].(string)] = true
		if n["session_name"] != "org/repo-7" {
			t.Errorf("session_name = %v", n["session_name"])
		}
	}
	for _, want := range []string{"state", "ci_status", "mergeable"} {
		if !types[want] {
			t.Errorf("missing change type %q", want)
		}
	}

	// "merged" is a terminal PR state: the transition still notifies
	// (asserted above), but the subscription itself is dropped immediately
	// afterward rather than kept around polling a resource that can never
	// change again — see TestPoller_TerminalStateDropsSubscription for the
	// dedicated coverage.
	subs, _ := store.All()
	if _, ok := subs[subKey("org/repo-7", resource)]; ok {
		t.Errorf("subscription must be dropped after observing a terminal state, got %+v", subs)
	}
	notified = nil
	p.Tick()
	if len(notified) != 0 {
		t.Errorf("dropped subscription must not be polled again, got %+v", notified)
	}
}

// A resource observed in a terminal state (a merged or closed PR, a closed
// issue) is dropped the moment it's observed — nothing further can ever
// change about it, so polling it again would only grow the registry and the
// steady-state gh call volume forever.
func TestPoller_TerminalStateDropsSubscription(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		routes   []ghRoute
	}{
		{
			name:     "merged PR",
			resource: "https://github.com/org/repo/pull/7",
			routes: []ghRoute{
				{match: "check-runs", stdout: ""},
				{match: "/status", stdout: ""},
				prCoreRoute("7", "closed", true, "deadbeef1"),
			},
		},
		{
			name:     "closed PR (not merged)",
			resource: "https://github.com/org/repo/pull/8",
			routes: []ghRoute{
				{match: "check-runs", stdout: ""},
				{match: "/status", stdout: ""},
				prCoreRoute("8", "closed", false, "deadbeef2"),
			},
		},
		{
			name:     "closed issue",
			resource: "https://github.com/org/repo/issues/9",
			routes: []ghRoute{
				{match: "issues/9", stdout: `{"state":"closed"}`},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir)
			if err := store.Subscribe(Subscription{SessionName: "org/repo-x", Resource: tt.resource}); err != nil {
				t.Fatal(err)
			}

			binDir := t.TempDir()
			gh := fakeGh(t, binDir, "", tt.routes)
			p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
			p.Tick()

			subs, _ := store.All()
			if _, ok := subs[subKey("org/repo-x", tt.resource)]; ok {
				t.Errorf("terminal resource must be dropped, got %+v", subs)
			}
		})
	}
}

// With Bus set, transitions are published as github.<change_type> events
// (Source=github, url in metadata) instead of POSTed to /notify.
func TestPoller_TickPublishesToBus(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	resource := "https://github.com/org/repo/pull/7"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7", Resource: resource}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-7", resource, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean", "draft": "false",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"conclusion":"success"}`},
		{match: "/status", stdout: ""},
		prCoreRouteFull("7", "closed", true, "deadbeef1", "dirty", false),
	})

	var published []event.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev event.Event
		json.NewDecoder(r.Body).Decode(&ev)
		published = append(published, ev)
		json.NewEncoder(w).Encode(map[string]any{"id": "01J", "offset": 0})
	}))
	defer srv.Close()

	p := &Poller{
		Store:     store,
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Bus:       &event.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		NotifyURL: "http://127.0.0.1:1/notify", // must be ignored when Bus is set
		GhBin:     gh,
		Guard:     ratebudget.NewGuard(t.TempDir()),
	}
	p.Tick()

	if len(published) != 3 {
		t.Fatalf("expected 3 published events, got %d: %+v", len(published), published)
	}
	types := map[string]bool{}
	for _, ev := range published {
		types[ev.Type] = true
		if ev.Source != sourceGitHub {
			t.Errorf("source = %q, want github", ev.Source)
		}
		if ev.SessionName != "org/repo-7" {
			t.Errorf("session_name = %q", ev.SessionName)
		}
		if ev.Metadata["url"] != resource {
			t.Errorf("metadata url = %q, want %q", ev.Metadata["url"], resource)
		}
		if ev.Metadata["resource"] != resource {
			t.Errorf("metadata resource = %q, want %q", ev.Metadata["resource"], resource)
		}
		if ev.Direction != event.Inbound {
			t.Errorf("direction = %q, want inbound", ev.Direction)
		}
	}
	for _, want := range []string{"github.state", "github.ci_status", "github.mergeable"} {
		if !types[want] {
			t.Errorf("missing published type %q (got %v)", want, types)
		}
	}
}

// The watcher.* event contract is minimal: type, summary, and
// metadata.url/metadata.resource only. No factual value (checks_status,
// mergeable_state, ...) may ride in the payload — consumers re-read current
// values from resource observe / dynamic outputs, never from this event.
func TestPoller_PublishedEventPayloadIsMinimal(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	resource := "https://github.com/org/repo/pull/7"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7", Resource: resource}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-7", resource, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean",
		"draft": "false", "head_sha": "abc1234",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"conclusion":"failure"}`},
		{match: "/status", stdout: ""},
		prCoreRouteFull("7", "open", false, "f00ba17cafef00d", "dirty", false),
	})

	var published []event.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev event.Event
		json.NewDecoder(r.Body).Decode(&ev)
		published = append(published, ev)
		json.NewEncoder(w).Encode(map[string]any{"id": "01J", "offset": 0})
	}))
	defer srv.Close()

	p := &Poller{
		Store:  store,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Bus:    &event.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		GhBin:  gh,
		Guard:  ratebudget.NewGuard(t.TempDir()),
	}
	p.Tick()

	if len(published) == 0 {
		t.Fatal("expected at least one published event")
	}
	for _, ev := range published {
		if ev.Type == "" || ev.Summary == "" {
			t.Errorf("event missing type/summary: %+v", ev)
		}
		if ev.Body != "" {
			t.Errorf("event body must be empty (payload is not the fact SSOT), got %q", ev.Body)
		}
		if len(ev.Metadata) != 2 || ev.Metadata["url"] == "" || ev.Metadata["resource"] == "" {
			t.Errorf("metadata must contain exactly url+resource, got %+v", ev.Metadata)
		}
	}
}

func TestPoller_SamePRPublishesPerSubscriber(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	resource := "https://github.com/org/repo/pull/7"
	subs := []Subscription{
		{SessionName: "org/repo-7", Resource: resource},
		{SessionName: "reviewer/sess", Resource: resource},
	}
	for _, sub := range subs {
		if err := store.Subscribe(sub); err != nil {
			t.Fatal(err)
		}
		// Pre-seed checks_status/mergeable_state/draft to match what the stub
		// below resolves so this tick's only transition is pr_state —
		// otherwise those probes resolving for the first time would
		// themselves count as transitions and inflate the published-event
		// count this test asserts.
		if err := store.SetLast(sub.SessionName, resource, map[string]string{
			"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean", "draft": "false",
		}); err != nil {
			t.Fatal(err)
		}
	}

	binDir := t.TempDir()
	callLog := filepath.Join(t.TempDir(), "gh-calls.log")
	gh := fakeGh(t, binDir, callLog, []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRouteFull("7", "closed", true, "deadbeef1", "clean", false),
	})

	var published []event.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev event.Event
		json.NewDecoder(r.Body).Decode(&ev)
		published = append(published, ev)
		json.NewEncoder(w).Encode(map[string]any{"id": "01J", "offset": len(published)})
	}))
	defer srv.Close()

	p := &Poller{
		Store:  store,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Bus:    &event.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		GhBin:  gh,
		Guard:  ratebudget.NewGuard(t.TempDir()),
	}
	p.Tick()

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal("gh was never invoked")
	}
	// The dedup contract is one FETCH per shared PR, so count the primary
	// per-PR REST probe calls.
	calls := strings.Count(string(data), "pulls/7 ")
	if calls != 1 {
		t.Errorf("expected 1 gh fetch for shared PR, got %d:\n%s", calls, data)
	}
	if len(published) != 2 {
		t.Fatalf("expected one state event per subscriber, got %d: %+v", len(published), published)
	}
	sessions := map[string]bool{}
	for _, ev := range published {
		if ev.Type != typeGitHubPrefix+"state" {
			t.Errorf("event type = %q, want github.state", ev.Type)
		}
		if ev.Metadata["resource"] != resource || ev.Metadata["url"] != resource {
			t.Errorf("metadata = %+v, want resource/url %q", ev.Metadata, resource)
		}
		sessions[ev.SessionName] = true
	}
	for _, want := range []string{"org/repo-7", "reviewer/sess"} {
		if !sessions[want] {
			t.Errorf("missing session %q in events %+v", want, published)
		}
	}
}

func TestPoller_InitialObservationDoesNotNotify(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Subscribe(Subscription{SessionName: "org/repo-8", Resource: "https://github.com/org/repo/pull/8"}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRoute("8", "open", false, "abc1234"),
	})

	notifyCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notifyCount++
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()
	if notifyCount != 0 {
		t.Errorf("first observation must establish a baseline silently, got %d notifications", notifyCount)
	}
	subs, _ := store.All()
	if subs[subKey("org/repo-8", "https://github.com/org/repo/pull/8")].Last["pr_state"] != "open" {
		t.Errorf("baseline = %+v", subs)
	}
}

func TestPoller_GhInvocationFailureLogsWarning(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Subscribe(Subscription{SessionName: "org/repo-9", Resource: "https://github.com/org/repo/pull/9"}); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	p := &Poller{
		Store:  store,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		// A nonexistent binary makes exec.Command's Output() fail before any
		// process runs at all, exercising the "gh itself could not be
		// invoked" branch rather than an ordinary non-2xx API response.
		GhBin: filepath.Join(t.TempDir(), "gh-does-not-exist"),
		Guard: ratebudget.NewGuard(t.TempDir()),
	}
	p.Tick()

	if !bytes.Contains(logs.Bytes(), []byte("gh api invocation failed")) {
		t.Errorf("expected a warning about the failed gh invocation, got log output: %q", logs.String())
	}
}

func TestPoller_DoesNotPruneSubscriptionsDuringPoll(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Subscribe(Subscription{SessionName: "org/repo-9", Resource: "https://github.com/org/repo/pull/9"}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRoute("9", "open", false, "abc1234"),
	})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	// Polling never probes plect state as a side task — session-existence
	// cleanup is left to explicit unsubscribe / Sweep. An open (non-terminal)
	// PR must also survive polling itself: only a resource observed in a
	// terminal state gets dropped inline (see
	// TestPoller_TerminalStateDropsSubscription).
	subs, _ := store.All()
	if len(subs) != 1 {
		t.Errorf("poll should leave subscription cleanup to unsubscribe, got %+v", subs)
	}
}

// A new head commit fires new_commits (github.new_comments/new_review_comments
// were removed — GitHub comments have no machine-consumer, so the watcher
// no longer fetches them at all).
func TestPoller_NewCommitsNotifies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/20"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-20", Resource: res}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-20", res, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "head_sha": "abc1234deadbeef",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRoute("20", "open", false, "f00ba17cafef00d"),
	})

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	got := map[string]string{}
	for _, n := range notified {
		got[n["change_type"].(string)] = n["summary"].(string)
	}
	if s, ok := got["new_commits"]; !ok || !strings.Contains(s, "f00ba17") {
		t.Errorf("new_commits = %q (notified=%+v)", s, notified)
	}
	subs, _ := store.All()
	if subs[subKey("org/repo-20", res)].Last["head_sha"] != "f00ba17cafef00d" {
		t.Errorf("head_sha baseline = %q, want f00ba17cafef00d", subs[subKey("org/repo-20", res)].Last["head_sha"])
	}
}

func TestChecksRollup(t *testing.T) {
	// Zero entries is a real PR whose checks haven't started (PENDING), not
	// the watcher's old empty-string "no CI at all" sentinel — this is the
	// the resource observation's checks rollup alignment requires.
	if got := checksRollup(nil); got != "PENDING" {
		t.Errorf("empty = %q, want PENDING", got)
	}
	failure := []statusCheck{{Conclusion: "success"}, {Conclusion: "failure"}}
	if got := checksRollup(failure); got != "FAILURE" {
		t.Errorf("failure = %q", got)
	}
	pending := []statusCheck{{Conclusion: "success"}, {Conclusion: ""}}
	if got := checksRollup(pending); got != "PENDING" {
		t.Errorf("pending = %q", got)
	}
	success := []statusCheck{{Conclusion: "success"}, {State: "success"}}
	if got := checksRollup(success); got != "SUCCESS" {
		t.Errorf("success = %q", got)
	}
	// A REST status-context entry's error state fails the rollup, same as a
	// check-run's failure conclusion would.
	statusError := []statusCheck{{State: "error"}}
	if got := checksRollup(statusError); got != "FAILURE" {
		t.Errorf("status error = %q, want FAILURE", got)
	}
}

func TestParseInterval(t *testing.T) {
	if d, err := ParseInterval(""); err != nil || d.Seconds() != 60 {
		t.Errorf("default = %v, %v", d, err)
	}
	if _, err := ParseInterval("1s"); err == nil {
		t.Error("sub-10s interval must be rejected")
	}
	if d, err := ParseInterval("5m"); err != nil || d.Minutes() != 5 {
		t.Errorf("5m = %v, %v", d, err)
	}
}

// Tag variants of the same issue on different branches track different
// linked PRs — the fetch dedupe must not let one branch's PR state leak
// into the other session.
func TestPoller_SameIssueDifferentBranchesDoNotShareObservation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	issue := "https://github.com/org/repo/issues/7"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7", Resource: issue, Branch: "issue/7"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7+debug", Resource: issue, Branch: "issue/7+debug"}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	// Linked-PR discovery answers per --head branch: issue/7 → PR 70 (merged),
	// issue/7+debug → PR 71 (still open). This needs branch-conditional logic
	// the generic ghRoute table can't express, so it's a bespoke stub. Each
	// branch's discovered PR number then gets its own follow-up pulls/{n} call.
	gh := fakeBin(t, binDir, "gh", `
printf 'HTTP/1.1 200 OK\n\n'
args="$*"
case "$args" in
  *"pulls/70 "*)
    echo '{"state":"closed","merged":true,"sha":"merged001","mergeable_state":"clean","draft":false}'
    ;;
  *"pulls/71 "*)
    echo '{"state":"open","merged":false,"sha":"open0001","mergeable_state":"clean","draft":false}'
    ;;
  *"issues/7 "*)
    echo '{"state":"open"}'
    ;;
  *"issue/7+debug"*)
    echo "71"
    ;;
  *"pulls "*)
    echo "70"
    ;;
esac
`)
	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	subs, _ := store.All()
	if got := subs[subKey("org/repo-7", issue)].Last["pr_state"]; got != "merged" {
		t.Errorf("org/repo-7 pr_state = %q, want merged", got)
	}
	if got := subs[subKey("org/repo-7+debug", issue)].Last["pr_state"]; got != "open" {
		t.Errorf("org/repo-7+debug pr_state = %q, want open (must not inherit the other branch's PR)", got)
	}
}

// Two sessions subscribing the SAME PR with different branches (dispatch-time
// auto-subscribe carries the PR's head branch; runtime `plect subscribe` carries
// none) must still poll once: a PR fetch ignores branch, so it is keyed on the
// resource alone. Guards the AC7 "poll dedup independent of branch" contract
// Guards poll dedup independent of branch.
func TestPoller_SamePRDifferentBranchesShareOneFetch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	pr := "https://github.com/org/repo/pull/7"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-7", Resource: pr, Branch: "feature/x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Subscribe(Subscription{SessionName: "reviewer/sess", Resource: pr, Branch: ""}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	callLog := filepath.Join(t.TempDir(), "gh-calls.log")
	gh := fakeGh(t, binDir, callLog, []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRoute("7", "open", false, "abc1234"),
	})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal("gh was never invoked")
	}
	// The dedup contract is one fetch per shared PR regardless of branch, so
	// count the primary per-PR REST probe calls.
	calls := strings.Count(string(data), "pulls/7 ")
	if calls != 1 {
		t.Errorf("expected 1 gh fetch for the shared PR, got %d:\n%s", calls, data)
	}
}

// A PR whose checks reset after a new push (the REST check-runs/status probes
// now return nothing) resolves to PENDING per the resource observation's
// checks_rollup — a real, notify-worthy transition, not the watcher's old
// silent "no CI at all" retraction.
func TestPoller_ChecksResetToPendingNotifies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res12 = "https://github.com/org/repo/pull/12"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-12", Resource: res12}); err != nil {
		t.Fatal(err)
	}
	// Baseline: checks failing.
	if err := store.SetLast("org/repo-12", res12, map[string]string{
		"pr_state": "open", "checks_status": "FAILURE",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRoute("12", "open", false, "abc1234"),
	})

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	if len(notified) != 1 {
		t.Fatalf("expected exactly 1 notification (ci_status), got %d: %+v", len(notified), notified)
	}
	if notified[0]["change_type"] != "ci_status" || !strings.Contains(notified[0]["summary"].(string), "PENDING") {
		t.Errorf("notification = %+v, want ci_status/PENDING", notified[0])
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-12", res12)].Last["checks_status"]; got != "PENDING" {
		t.Errorf("checks_status baseline = %q, want PENDING", got)
	}
}

// A mergeable_state transition (clean → dirty, e.g. a conflicting rebase)
// fires github.mergeable — the signal added to
// cover the known blind spot where a conflicting PR gets no CI run at all and
// so never fires github.ci_status.
func TestPoller_MergeableStateTransitionNotifies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/50"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-50", Resource: res}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-50", res, map[string]string{
		"pr_state": "open", "checks_status": "SUCCESS", "mergeable_state": "clean", "draft": "false",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"conclusion":"success"}`},
		{match: "/status", stdout: ""},
		prCoreRouteFull("50", "open", false, "abc1234", "dirty", false),
	})

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	if len(notified) != 1 {
		t.Fatalf("expected exactly 1 notification (mergeable), got %d: %+v", len(notified), notified)
	}
	if notified[0]["change_type"] != "mergeable" || !strings.Contains(notified[0]["summary"].(string), "dirty") {
		t.Errorf("notification = %+v, want mergeable/dirty", notified[0])
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-50", res)].Last["mergeable_state"]; got != "dirty" {
		t.Errorf("mergeable_state baseline = %q, want dirty", got)
	}
}

// GitHub reporting mergeable_state "unknown" (still computing) must neither
// notify nor overwrite the last real value — otherwise a PR would flap
// clean→unknown→clean on every poll while GitHub recomputes mergeability
// (the flapping-prevention requirement).
func TestPoller_MergeableUnknownDoesNotUpdateOrNotify(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/51"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-51", Resource: res}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-51", res, map[string]string{
		"pr_state": "open", "checks_status": "SUCCESS", "mergeable_state": "clean", "draft": "false",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"conclusion":"success"}`},
		{match: "/status", stdout: ""},
		prCoreRouteFull("51", "open", false, "abc1234", "unknown", false),
	})

	notifyCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { notifyCount++ }))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	if notifyCount != 0 {
		t.Errorf("mergeable_state=unknown must not notify, got %d", notifyCount)
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-51", res)].Last["mergeable_state"]; got != "clean" {
		t.Errorf("mergeable_state baseline must stay clean while GitHub recomputes, got %q", got)
	}
}

// A PR leaving draft (draft → ready for review) fires github.draft.
func TestPoller_DraftBecomesReadyForReviewNotifies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/52"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-52", Resource: res}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-52", res, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean", "draft": "true",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRouteFull("52", "open", false, "abc1234", "clean", false),
	})

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	if len(notified) != 1 {
		t.Fatalf("expected exactly 1 notification (draft), got %d: %+v", len(notified), notified)
	}
	if notified[0]["change_type"] != "draft" || notified[0]["summary"] != "PR ready for review" {
		t.Errorf("notification = %+v, want draft/\"PR ready for review\"", notified[0])
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-52", res)].Last["draft"]; got != "false" {
		t.Errorf("draft baseline = %q, want false", got)
	}
}

// A PR converted back to draft is the opposite, non-actionable direction: the
// baseline advances silently (no notification, no stuck value).
func TestPoller_DraftGoingBackToDraftDoesNotNotify(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/53"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-53", Resource: res}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-53", res, map[string]string{
		"pr_state": "open", "checks_status": "PENDING", "mergeable_state": "clean", "draft": "false",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRouteFull("53", "open", false, "abc1234", "clean", true),
	})

	notifyCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { notifyCount++ }))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	if notifyCount != 0 {
		t.Errorf("reverting to draft must not notify, got %d", notifyCount)
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-53", res)].Last["draft"]; got != "true" {
		t.Errorf("draft baseline = %q, want true (advances silently)", got)
	}
}

// An issue session's linked PR now needs a follow-up pulls/{n} call beyond
// discovery, since mergeable_state/draft aren't in the pulls?head= list
// response — verify both land in the baseline.
func TestPoller_LinkedPRMergeableAndDraftResolved(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/issues/40"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-40", Resource: res, Branch: "issue/40"}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		issueCoreRoute("40", "open"),
		linkedPRDiscoveryRoute("70"),
		{match: "check-runs", stdout: ""},
		{match: "/status", stdout: ""},
		prCoreRouteFull("70", "open", false, "abc1234", "dirty", true),
	})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	subs, _ := store.All()
	last := subs[subKey("org/repo-40", res)].Last
	if last["pr_state"] != "open" {
		t.Errorf("pr_state = %q, want open", last["pr_state"])
	}
	if last["mergeable_state"] != "dirty" {
		t.Errorf("mergeable_state = %q, want dirty", last["mergeable_state"])
	}
	if last["draft"] != "true" {
		t.Errorf("draft = %q, want true", last["draft"])
	}
}

// A since-unlinked PR (successful discovery, no PR found) must retract EVERY
// PR-scoped field to empty/zero — not just pr_state/head_sha — or a stale
// checks_status/mergeable_state/draft from the old linked PR freezes forever.
func TestPoller_LinkedPRUnlinkRetractsAllFields(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/issues/41"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-41", Resource: res, Branch: "issue/41"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-41", res, map[string]string{
		"issue_state": "open", "pr_state": "open", "checks_status": "PENDING",
		"mergeable_state": "clean", "draft": "true", "head_sha": "abc1234",
	}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		issueCoreRoute("41", "open"),
		linkedPRDiscoveryRoute(""), // successful lookup, no PR found
	})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	subs, _ := store.All()
	last := subs[subKey("org/repo-41", res)].Last
	// draft has no empty-string equivalent (it's a bool): "false" is its
	// neutral/retracted representation, which is safe — the draft→ready
	// notification only ever fires from a literal old=="true", so a fresh
	// "false" here never causes a future misfire (unlike leaving "true").
	if last["pr_state"] != "" || last["checks_status"] != "" || last["mergeable_state"] != "" || last["draft"] != "false" || last["head_sha"] != "" {
		t.Errorf("unlink must retract every PR-scoped field, got %+v", last)
	}
}

// A FAILED linked-PR lookup must not retract previously observed values —
// only a successful (possibly empty) lookup is authoritative.
func TestPoller_FailedLinkedPRLookupDoesNotRetract(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res13 = "https://github.com/org/repo/issues/13"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-13", Resource: res13, Branch: "issue/13"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLast("org/repo-13", res13, map[string]string{"issue_state": "open", "pr_state": "open"}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "pulls", fail: true},
		issueCoreRoute("13", "open"),
	})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	p.Tick()

	subs, _ := store.All()
	if got := subs[subKey("org/repo-13", res13)].Last["pr_state"]; got != "open" {
		t.Errorf("failed lookup must keep the stale value, got %q", got)
	}
}

// --- ETag conditional GET + shared rate guard ---

// httpFakeGh writes a `gh` stub that speaks raw HTTP/1.1 status lines,
// headers, and body verbatim (unlike fakeGh, which always synthesizes a bare
// 200) — for scenarios that need to control status code and headers
// (If-None-Match echoing, 403 with rate-limit headers).
func httpFakeGh(t *testing.T, dir, script string) string {
	t.Helper()
	return fakeBin(t, dir, "gh", script)
}

// A second poll of an unchanged resource sends If-None-Match and, on 304,
// must resolve the SAME observation from the cached body rather than
// treating the fetch as failed: a 304 is a successful "unchanged" answer,
// not an error.
func TestPoller_ConditionalGetReusesCachedBodyOn304(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/100"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-100", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	callLog := filepath.Join(t.TempDir(), "gh-calls.log")
	// First call (no If-None-Match yet) answers 200 with an ETag; any later
	// call carrying that ETag answers 304 with no body.
	gh := httpFakeGh(t, binDir, `
echo "$@" >> `+callLog+`
args="$*"
case "$args" in
  *"pulls/100 "*)
    if [[ "$args" == *'If-None-Match: "v1"'* ]]; then
      printf 'HTTP/1.1 304 Not Modified\r\nEtag: "v1"\r\n\r\n'
      exit 1
    fi
    printf 'HTTP/1.1 200 OK\r\nEtag: "v1"\r\n\r\n{"state":"open","merged":false,"sha":"abc1234","mergeable_state":"clean","draft":false}'
    exit 0
    ;;
  *"check-runs"*|*"/status"*)
    printf 'HTTP/1.1 200 OK\r\n\r\n'
    exit 0
    ;;
esac
printf 'HTTP/1.1 200 OK\r\n\r\n'
`)

	guardDir := t.TempDir()
	var logs bytes.Buffer
	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(&logs, nil)), GhBin: gh, Guard: ratebudget.NewGuard(guardDir)}
	p.Tick() // seeds the baseline + ETag cache from the 200 response
	p.Tick() // must hit the cache via 304

	subs, _ := store.All()
	if got := subs[subKey("org/repo-100", res)].Last["pr_state"]; got != "open" {
		t.Fatalf("pr_state = %q, want open (304 must resolve from cache, not fail)", got)
	}
	etag, body, ok := store.GetCache("repos/org/repo/pulls/100")
	if !ok || etag != `"v1"` || body == "" {
		t.Errorf("cache = etag=%q body=%q ok=%v, want persisted v1/non-empty", etag, body, ok)
	}

	data, _ := os.ReadFile(callLog)
	if !strings.Contains(string(data), `If-None-Match: "v1"`) {
		t.Errorf("second tick must send If-None-Match, calls:\n%s", data)
	}

	// The 304's empty body makes gh exit non-zero (its --jq filter has
	// nothing to project), even though this is a normal cache hit — that
	// exit code alone must never produce a warning, or every unchanged
	// resource would warn-spam the journal on every tick.
	if strings.Contains(logs.String(), "gh api invocation failed") {
		t.Errorf("a 304 cache hit must not log an invocation-failed warning, got:\n%s", logs.String())
	}
}

// A 403/429 response must set the shared backoff guard from its
// Retry-After header, and a caller that checks the guard before that window
// elapses must not invoke gh at all. This exercises the same guard the
// poller's fetch path consults, end-to-end through Tick.
func TestPoller_ThrottleSetsGuardAndSkipsSubsequentFetch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/101"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-101", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	callLog := filepath.Join(t.TempDir(), "gh-calls.log")
	gh := httpFakeGh(t, binDir, `
echo "$@" >> `+callLog+`
printf 'HTTP/1.1 403 Forbidden\r\nRetry-After: 120\r\n\r\n{"message":"secondary rate limit"}'
exit 1
`)

	guardDir := t.TempDir()
	guard := ratebudget.NewGuard(guardDir)
	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: guard}
	p.Tick()

	wait, err := guard.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if wait < 110*time.Second || wait > 120*time.Second {
		t.Fatalf("guard wait = %v, want ~120s (Retry-After honored)", wait)
	}

	calls, _ := os.ReadFile(callLog)
	before := strings.Count(string(calls), "\n")

	p.Tick() // must not call gh at all: the guard is still backed off

	after, _ := os.ReadFile(callLog)
	afterCount := strings.Count(string(after), "\n")
	if afterCount != before {
		t.Errorf("second tick invoked gh %d more time(s) while backed off, want 0 (log:\n%s)", afterCount-before, after)
	}
}

// --- Fail-streak surfacing: loud once, not spammed every tick ---

// A subscription whose fetch keeps failing is surfaced exactly once, the
// moment it crosses failureSurfaceThreshold consecutive failures — not
// before (a transient blip must stay silent) and not again afterward (an
// unresolved problem must stay visible without repeating into a journal
// nobody reads).
func TestPoller_RepeatedFailureSurfacesOnceAtThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/issues/50"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-50", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{{match: "issues/50", fail: true}})

	var notified []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		notified = append(notified, body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NotifyURL: srv.URL, GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}

	for i := 0; i < failureSurfaceThreshold-1; i++ {
		p.Tick()
	}
	if len(notified) != 0 {
		t.Fatalf("must not surface before the threshold, got %+v", notified)
	}

	p.Tick() // the Nth consecutive failure
	if len(notified) != 1 {
		t.Fatalf("expected exactly one surfaced notification at the threshold, got %d: %+v", len(notified), notified)
	}
	if notified[0]["change_type"] != "fetch_failed" {
		t.Errorf("change_type = %v, want fetch_failed", notified[0]["change_type"])
	}

	p.Tick() // one more failure past the threshold
	if len(notified) != 1 {
		t.Errorf("must not re-surface every subsequent failure, got %d: %+v", len(notified), notified)
	}
}

// A fail streak resets on the next successful poll, so a later, unrelated
// blip doesn't compound toward the surface threshold with a stale count.
func TestPoller_FailureStreakResetsOnSuccess(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/issues/51"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-51", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	failFlag := filepath.Join(t.TempDir(), "fail-mode")
	if err := os.WriteFile(failFlag, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	gh := fakeBin(t, binDir, "gh", `
if [[ "$(cat `+failFlag+`)" == "1" ]]; then
  printf 'HTTP/1.1 500 Internal Server Error\n\n'
  exit 1
fi
printf 'HTTP/1.1 200 OK\n\n{"state":"open"}'
`)

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}

	for i := 0; i < failureSurfaceThreshold-1; i++ {
		p.Tick()
	}
	subs, _ := store.All()
	if got := subs[subKey("org/repo-51", res)].FailStreak; got != failureSurfaceThreshold-1 {
		t.Fatalf("fail streak = %d, want %d", got, failureSurfaceThreshold-1)
	}

	if err := os.WriteFile(failFlag, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Tick() // succeeds
	subs, _ = store.All()
	if got := subs[subKey("org/repo-51", res)].FailStreak; got != 0 {
		t.Errorf("fail streak = %d, want reset to 0 after a success", got)
	}
}

// --- Sweep: drop subscriptions for destroyed sessions ---

// fakePlectLs writes a `plect` stub that answers `ls --json` with the given
// session names, mirroring the shape Sweep parses (session_name only — run
// state is irrelevant to session existence).
func fakePlectLs(t *testing.T, dir string, sessionNames []string) string {
	t.Helper()
	rows := make([]string, len(sessionNames))
	for i, n := range sessionNames {
		rows[i] = fmt.Sprintf(`{"session_name":%q}`, n)
	}
	return fakeBin(t, dir, "plect", "echo '["+strings.Join(rows, ",")+"]'\n")
}

func TestPoller_SweepDropsSubscriptionsForDestroyedSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const liveRes = "https://github.com/org/repo/pull/1"
	const deadRes = "https://github.com/org/repo/pull/2"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-1", Resource: liveRes}); err != nil {
		t.Fatal(err)
	}
	if err := store.Subscribe(Subscription{SessionName: "org/repo-2", Resource: deadRes}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	plect := fakePlectLs(t, binDir, []string{"org/repo-1"}) // org/repo-2 no longer exists

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), PlectBin: plect}
	p.Sweep()

	subs, _ := store.All()
	if _, ok := subs[subKey("org/repo-1", liveRes)]; !ok {
		t.Error("live session's subscription must survive Sweep")
	}
	if _, ok := subs[subKey("org/repo-2", deadRes)]; ok {
		t.Error("destroyed session's subscription must be dropped by Sweep")
	}
}

// A session plect still lists as "down" (not destroyed — resumable via
// `plect up`) must not be swept; only a session plect no longer knows about
// at all is gone.
func TestPoller_SweepKeepsDownButNotDestroyedSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/3"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-3", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	plect := fakePlectLs(t, binDir, []string{"org/repo-3"})

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), PlectBin: plect}
	p.Sweep()

	subs, _ := store.All()
	if _, ok := subs[subKey("org/repo-3", res)]; !ok {
		t.Error("a session plect still lists (even if down) must survive Sweep")
	}
}

// A `plect` that can't be asked (missing binary, non-zero exit) must never
// be treated as "nothing exists" — that would sweep every live session's
// subscriptions on a transient hiccup.
func TestPoller_SweepSkipsWhenPlectListFails(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/4"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-4", Resource: res}); err != nil {
		t.Fatal(err)
	}

	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), PlectBin: filepath.Join(t.TempDir(), "plect-does-not-exist")}
	p.Sweep()

	subs, _ := store.All()
	if _, ok := subs[subKey("org/repo-4", res)]; !ok {
		t.Error("Sweep must leave subscriptions untouched when it can't list sessions")
	}
}

// A hung `plect ls` (e.g. one session's movement-source healthcheck never
// returning) must not block Sweep — and therefore the watcher's first
// Tick — forever. Bounding the subprocess with PlectListTimeout is what
// turns "hangs indefinitely" into "fails like any other liveSessions error."
func TestPoller_SweepBoundsHungPlectListCall(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	const res = "https://github.com/org/repo/pull/5"
	if err := store.Subscribe(Subscription{SessionName: "org/repo-5", Resource: res}); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	plect := fakeBin(t, binDir, "plect", "sleep 30\n") // never returns within any sane test budget

	p := &Poller{
		Store:            store,
		Logger:           slog.New(slog.NewTextHandler(os.Stderr, nil)),
		PlectBin:         plect,
		PlectListTimeout: 200 * time.Millisecond,
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		p.Sweep()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Sweep did not return within 5s of a hung plect ls — the timeout isn't bounding the subprocess")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Sweep took %v to return, want close to the 200ms PlectListTimeout", elapsed)
	}

	subs, _ := store.All()
	if _, ok := subs[subKey("org/repo-5", res)]; !ok {
		t.Error("Sweep must leave subscriptions untouched when plect ls times out")
	}
}

func TestLatestCheckPerName_SupersededRuns(t *testing.T) {
	tests := []struct {
		name   string
		checks []statusCheck
		want   string
	}{
		{
			name: "cancelled run superseded by a later success",
			checks: []statusCheck{
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T11:00:00Z", ID: 2},
			},
			want: "SUCCESS",
		},
		{
			name: "identical start times fall back to the higher run id",
			checks: []statusCheck{
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T10:00:00Z", ID: 9},
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 4},
			},
			want: "SUCCESS",
		},
		{
			name: "latest run failing still fails the rollup",
			checks: []statusCheck{
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "build", Conclusion: "failure", StartedAt: "2026-08-20T11:00:00Z", ID: 2},
			},
			want: "FAILURE",
		},
		{
			name: "a check name with only cancelled runs still fails the rollup",
			checks: []statusCheck{
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T11:00:00Z", ID: 1},
				{Name: "lint", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 2},
			},
			want: "FAILURE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checksRollup(latestCheckPerName(tt.checks)); got != tt.want {
				t.Errorf("checksRollup(latestCheckPerName(...)) = %q, want %q", got, tt.want)
			}
		})
	}
}

// A combined-status entry carries no check-run name, so name-based deduping
// must not collapse distinct status contexts into one.
func TestLatestCheckPerName_KeepsCombinedStatusEntries(t *testing.T) {
	checks := []statusCheck{{State: "success"}, {State: "error"}}
	if got := checksRollup(append(latestCheckPerName(nil), checks...)); got != "FAILURE" {
		t.Errorf("rollup = %q, want FAILURE", got)
	}
}

func TestFetchChecks_SupersededCancelledRunDoesNotFail(t *testing.T) {
	binDir := t.TempDir()
	gh := fakeGh(t, binDir, "", []ghRoute{
		{match: "check-runs", stdout: `{"id":1,"name":"build","conclusion":"cancelled","started_at":"2026-08-20T10:00:00Z"}
{"id":2,"name":"build","conclusion":"success","started_at":"2026-08-20T11:00:00Z"}`},
		{match: "/status", stdout: ""},
	})
	p := &Poller{
		Store:  NewStore(t.TempDir()),
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
		GhBin:  gh,
		Guard:  ratebudget.NewGuard(t.TempDir()),
	}
	got, ok := p.fetchChecks("org", "repo", "deadbeef")
	if !ok {
		t.Fatal("fetchChecks failed")
	}
	if got != "SUCCESS" {
		t.Errorf("checks = %q, want SUCCESS", got)
	}
}
