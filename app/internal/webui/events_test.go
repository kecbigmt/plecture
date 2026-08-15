package webui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/contracts/event"
)

func TestEventsPartial_RendersNewestFirst(t *testing.T) {
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	svc := &fakeService{events: []event.Event{
		{Type: "github.ci_status", Source: "github", Summary: "older", Time: t0},
		{Type: "claude.reply", Source: "claude", Summary: "newer", Time: t0.Add(time.Minute)},
	}}
	rr := get(t, svc, "/events?session=octocat/hello-world-42")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"github.ci_status", "claude.reply", "older", "newer"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// newest-first: claude.reply (newer) should appear before github.ci_status
	if strings.Index(body, "claude.reply") > strings.Index(body, "github.ci_status") {
		t.Errorf("expected newest-first ordering")
	}
}

func TestEventsPartial_EmptyState(t *testing.T) {
	rr := get(t, &fakeService{}, "/events?session=o/r-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No events yet") {
		t.Errorf("expected empty-state message, got %q", rr.Body.String())
	}
}

func TestEventsPartial_RequiresSession(t *testing.T) {
	rr := get(t, &fakeService{}, "/events")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// Cross-session scopes come from the service already newest-first (the desc
// page); the handler must render them as-is, not reverse like the session tail.
func TestEventsPartial_SubtreeScope(t *testing.T) {
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	svc := &fakeService{subtreeEvents: []event.Event{
		{SessionName: "o/r-2", Type: "claude.reply", Summary: "newer", Time: t0.Add(time.Minute)},
		{SessionName: "o/r-1", Type: "lifecycle.created", Summary: "older", Time: t0},
	}}
	rr := get(t, svc, "/events?subtree=o%2Fr-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if svc.gotSubtree != "o/r-1" {
		t.Errorf("subtree root = %q, want o/r-1", svc.gotSubtree)
	}
	body := rr.Body.String()
	if strings.Index(body, "newer") > strings.Index(body, "older") {
		t.Errorf("subtree rows must stay newest-first (no re-reversal)")
	}
	// The merged view interleaves sessions, so each row links its session.
	if !strings.Contains(body, `href="/sessions/o/r-2"`) {
		t.Errorf("merged rows should link their session, got %q", body)
	}
}

func TestEventsPartial_ScopesMutuallyExclusive(t *testing.T) {
	for _, path := range []string{
		"/events?session=o/r-1&subtree=o/r-1",
	} {
		if rr := get(t, &fakeService{}, path); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rr.Code)
		}
	}
}

func TestSubtreeTimelinePage(t *testing.T) {
	svc := &fakeService{subtreeEvents: []event.Event{
		{SessionName: "o/r-2", Type: "claude.reply", Summary: "from child", Time: time.Now()},
	}}
	rr := get(t, svc, "/subtrees/o/r-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(strings.ToLower(body), "<html") {
		t.Errorf("subtree timeline should be a full page")
	}
	for _, want := range []string{
		"Subtree timeline",
		`href="/sessions/o/r-1"`,           // back to the root session
		`hx-get="/events?subtree=o%2Fr-1"`, // polling refresh reuses the partial
		"from child",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subtree page missing %q", want)
		}
	}
}

func TestSubtreeTimelinePage_RootNotFound(t *testing.T) {
	svc := &fakeService{subtreeErr: &service.Error{Code: service.ErrSessionNotFound, Message: "no session"}}
	if rr := get(t, svc, "/subtrees/o/gone-9"); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// The detail page projects the session tree (parent/children) and the
// canonical subtree timeline link.
func TestDetail_ShowsTree(t *testing.T) {
	status := sampleShow()
	status.Identity.ParentSession = "owner/repo-1"
	status.Identity.Children = []string{"owner/repo-8", "owner/repo-9"}
	rr := get(t, &fakeService{status: status}, "/sessions/owner/repo-7")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`href="/sessions/owner/repo-1"`,
		`href="/sessions/owner/repo-8"`,
		`href="/sessions/owner/repo-9"`,
		`href="/subtrees/owner/repo-7"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}

func TestDetail_IncludesLiveTimeline(t *testing.T) {
	svc := nameCapture{fn: func(string) {}, status: sampleShow()}
	rr := get(t, svc, "/sessions/owner/repo-7")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// The timeline is SSE-driven: a hidden #event-stream carries the stream URL
	// (the shell script opens an EventSource to it) and #event-list receives the
	// prepended rows. The emit form posts to /events.
	for _, want := range []string{
		`id="event-stream"`,
		`data-url="/events/stream?session=`,
		`id="event-list"`,
		`hx-post="/events"`,
		`name="type" value="user.emit"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}

	// Regression: the SSE URL must be encoded exactly once. Pre-escaping the
	// session before html/template re-escapes the attribute double-encodes the
	// "/" (%2F→%252F), so the endpoint queries a non-existent session and the
	// timeline silently stays empty.
	if strings.Contains(body, "%252") {
		t.Errorf("event-stream URL is double-encoded (contains %%252): %s", body)
	}
	gotURL := between(body, `data-url="`, `"`)
	u, err := url.Parse(gotURL)
	if err != nil {
		t.Fatalf("data-url %q: %v", gotURL, err)
	}
	if got := u.Query().Get("session"); got != "owner/repo-7" {
		t.Errorf("data-url session = %q, want owner/repo-7 (single-encoded)", got)
	}
}

// between returns the substring of s between the first occurrence of pre and the
// next occurrence of suf after it.
func between(s, pre, suf string) string {
	i := strings.Index(s, pre)
	if i < 0 {
		return ""
	}
	s = s[i+len(pre):]
	if j := strings.Index(s, suf); j >= 0 {
		return s[:j]
	}
	return s
}
