package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/contracts/event"
)

// appView is the data for the two-pane shell (list on the left, detail on the
// right). The same shell backs both "/" and "/sessions/<name>": Detail is the
// selected session (nil = none), NotFound names a missing one. Cards carry the
// active flag so the selected row is highlighted, and the token feeds the
// create form's CSRF header.
type appView struct {
	Cards     []cardView
	Order     string // "recent" | "oldest"
	CSRFToken string
	Detail    *service.StatusResult
	NotFound  string
}

// cardView is a list entry plus whether it is the currently-open session. The
// entry is embedded so the card template's field references (.SessionName, …)
// keep working via promotion.
type cardView struct {
	service.ListEntry
	Active bool
}

// toCards marks the row matching active (the open session) so the template can
// highlight it.
func toCards(entries []service.ListEntry, active string) []cardView {
	cards := make([]cardView, len(entries))
	for i, e := range entries {
		cards[i] = cardView{ListEntry: e, Active: e.SessionName == active}
	}
	return cards
}

// handleIndex renders the shell with an empty detail pane.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.shellView(w, r, nil, ""); ok {
		s.render(w, "list", v)
	}
}

// handleSessionsPartial renders just the rows fragment for the htmx auto-refresh.
// It reads the open session from the browser's current URL so the refresh keeps
// the selected row highlighted.
func (s *Server) handleSessionsPartial(w http.ResponseWriter, r *http.Request) {
	entries, ok := s.listEntries(w, r)
	if !ok {
		return
	}
	s.render(w, "rows", toCards(entries, selectedFromCurrentURL(r)))
}

// handleSessionDetail serves one session. An htmx request (a card click) gets
// just the detail fragment to swap into #detail-pane; the clicked row is
// highlighted instantly client-side and reaffirmed by the list's periodic
// refresh (which reads the open session from HX-Current-URL). A plain navigation
// (deep link, reload, no-JS) gets the full shell with the detail already in the
// right pane. A missing session is a 404 (an expected stale-link outcome), not
// a 500.
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	res, err := s.svc.Status(name)
	if err != nil {
		var svcErr *service.Error
		notFound := errors.As(err, &svcErr) && svcErr.Code == service.ErrWorkspaceNotFound
		switch {
		case notFound && isHTMX(r):
			s.renderStatus(w, http.StatusNotFound, "detail-notfound-pane", name)
		case notFound:
			if v, ok := s.shellView(w, r, nil, name); ok {
				s.renderStatus(w, http.StatusNotFound, "list", v)
			}
		default:
			s.renderError(w, err)
		}
		return
	}
	if isHTMX(r) {
		s.render(w, "detail-pane", res)
		return
	}
	if v, ok := s.shellView(w, r, res, ""); ok {
		s.render(w, "list", v)
	}
}

// shellView assembles the two-pane shell: the sorted list (with the open session
// highlighted) plus the right-pane state. Returns ok=false after writing a 500
// when the list can't be loaded.
func (s *Server) shellView(w http.ResponseWriter, r *http.Request, detail *service.StatusResult, notFound string) (appView, bool) {
	entries, ok := s.listEntries(w, r)
	if !ok {
		return appView{}, false
	}
	active := notFound
	if detail != nil {
		active = detail.Identity.SessionName
	}
	return appView{
		Cards:     toCards(entries, active),
		Order:     orderName(newestFirst(r)),
		CSRFToken: s.ensureCSRFToken(w, r),
		Detail:    detail,
		NotFound:  notFound,
	}, true
}

// isHTMX reports whether the request came from htmx (a card click into the
// detail pane) rather than a plain browser navigation.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// selectedFromCurrentURL extracts the open session name from htmx's
// HX-Current-URL header (the browser's address bar), so the polled rows fragment
// can re-highlight it. Empty when the list root is open.
func selectedFromCurrentURL(r *http.Request) string {
	u, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err != nil {
		return ""
	}
	const prefix = "/sessions/"
	if !strings.HasPrefix(u.Path, prefix) {
		return ""
	}
	name, err := url.PathUnescape(strings.TrimPrefix(u.Path, prefix))
	if err != nil {
		return ""
	}
	return name
}

// handleSessionEvents renders the event timeline partial, newest first. The
// scope is exactly one of session (one log) or subtree (the tree rooted at a
// session — the canonical cross-session view), mirroring sennit_event_list; it
// rides as a query param (a session name's "/" would otherwise collide with
// the {name...} detail route).
func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	session, subtree := q.Get("session"), q.Get("subtree")
	scopes := 0
	for _, v := range []string{session, subtree} {
		if v != "" {
			scopes++
		}
	}
	if scopes == 0 {
		http.Error(w, "session or subtree is required", http.StatusBadRequest)
		return
	}
	if scopes > 1 {
		http.Error(w, "session and subtree are mutually exclusive", http.StatusBadRequest)
		return
	}
	var evs []event.Event
	var err error
	switch {
	case subtree != "":
		evs, err = s.svc.EventsSubtree(subtree)
	default:
		evs, err = s.svc.Events(session)
		slices.Reverse(evs) // the single-session tail is oldest-first
	}
	if err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	s.render(w, "event-rows", evs)
}

// timelineView is the data for the cross-session timeline page: the scope, the
// initial rows (newest first), and the partial URL the page polls to refresh.
// Root is the subtree's root session.
type timelineView struct {
	Root    string
	PollURL string
	Events  []event.Event
}

// handleSubtreeTimeline serves the canonical cross-session timeline for the
// session tree rooted at the named session (the root plus its descendants,
// merged in event-id order — the same read as `sennit event list --subtree`).
func (s *Server) handleSubtreeTimeline(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	evs, err := s.svc.EventsSubtree(name)
	if err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	s.render(w, "timeline", timelineView{
		Root:    name,
		PollURL: "/events?subtree=" + url.QueryEscape(name),
		Events:  evs,
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "ok")
}

// handleSessionCreate creates and starts a session from the form's URL (the
// docker-compose-up-style path: Up auto-creates then runs run-scoped tasks).
// Success redirects to the new session's detail page; a service error becomes a
// status-mapped banner swapped next to the form.
//
// workflow is forwarded like the CLI/MCP surfaces, but service.Up only
// auto-creates for a resolver-matched or URL-shaped identifier — so unlike
// `sennit create`/`sennit_create`, this form cannot create a resolver-less session
// from a bare identifier. Here workflow only disambiguates multiple
// workflows matching the same resolver.
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.FormValue("url"))
	if rawURL == "" {
		s.renderStatusError(w, http.StatusBadRequest, "URL is required")
		return
	}
	inputs, err := parseInputs(r.FormValue("inputs"))
	if err != nil {
		s.renderStatusError(w, http.StatusBadRequest, err.Error())
		return
	}
	inputs, err = service.MergeTaskInput(inputs, strings.TrimSpace(r.FormValue("task")))
	if err != nil {
		s.renderStatusError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.svc.Up(service.UpParams{
		Identifier: rawURL,
		Tag:        strings.TrimSpace(r.FormValue("tag")),
		Workflow:   strings.TrimSpace(r.FormValue("workflow")),
		Inputs:     inputs,
	})
	if err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	hxRedirect(w, sessionPath(res.SessionName))
}

// handleSessionUp runs run-scoped setup for the named session.
func (s *Server) handleSessionUp(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if _, err := s.svc.Up(service.UpParams{Identifier: name}); err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	redirectAfterAction(w, r, sessionPath(name))
}

// handleSessionDown runs run-scoped cleanup for the named session.
func (s *Server) handleSessionDown(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if _, err := s.svc.Down(service.DownParams{Identifier: name}); err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	redirectAfterAction(w, r, sessionPath(name))
}

// handleSessionDestroy tears the session down. With cleanup warnings (only
// possible under --force) it renders them in place instead of redirecting, so
// they are not lost; a clean teardown redirects to the list.
func (s *Server) handleSessionDestroy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	res, err := s.svc.Destroy(service.DestroyParams{
		Identifier:   name,
		Force:        r.FormValue("force") != "",
		DeleteBranch: r.FormValue("delete_branch") != "",
	})
	if err != nil {
		s.renderStatusError(w, httpStatusForError(err), err.Error())
		return
	}
	if len(res.CleanupWarnings) > 0 || res.WorktreeWarning != "" {
		s.render(w, "destroy-warnings", res)
		return
	}
	hxRedirect(w, "/")
}

// parseInputs turns the optional inputs textarea into the create params map.
// Empty stays nil so the service distinguishes "no inputs" from "{}".
func parseInputs(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("inputs must be a JSON object: %v", err)
	}
	if out == nil {
		return nil, fmt.Errorf("inputs must be a JSON object, got null")
	}
	return out, nil
}

// hxRedirect tells htmx to navigate to target after a successful mutation. The
// body stays empty: htmx acts on the header and never swaps.
func hxRedirect(w http.ResponseWriter, target string) {
	w.Header().Set("HX-Redirect", target)
	w.WriteHeader(http.StatusOK)
}

// redirectAfterAction returns the user to wherever they triggered the action
// (the detail page or the list, via htmx's current-URL header) so up/down keep
// context, falling back to the session's detail page.
func redirectAfterAction(w http.ResponseWriter, r *http.Request, fallback string) {
	target := r.Header.Get("HX-Current-URL")
	if target == "" {
		target = fallback
	}
	hxRedirect(w, target)
}

// listEntries fetches and sorts the sessions for the request's order, or writes
// a 500 banner and returns ok=false on failure.
func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) ([]service.ListEntry, bool) {
	entries, err := s.svc.List()
	if err != nil {
		s.renderError(w, err)
		return nil, false
	}
	sortByActivity(entries, newestFirst(r))
	return entries, true
}

// newestFirst is the default; ?order=oldest reverses it.
func newestFirst(r *http.Request) bool {
	return r.URL.Query().Get("order") != "oldest"
}

func orderName(newest bool) string {
	if newest {
		return "recent"
	}
	return "oldest"
}

// sortByActivity orders sessions by last-active time (newest or oldest first).
// Untracked sessions have no timestamp and always sort last; ties break by name.
func sortByActivity(entries []service.ListEntry, newest bool) {
	slices.SortFunc(entries, func(a, b service.ListEntry) int {
		switch {
		case a.LastActiveAt == nil && b.LastActiveAt == nil:
			return strings.Compare(a.SessionName, b.SessionName)
		case a.LastActiveAt == nil:
			return 1
		case b.LastActiveAt == nil:
			return -1
		case a.LastActiveAt.Equal(*b.LastActiveAt):
			return strings.Compare(a.SessionName, b.SessionName)
		}
		older := a.LastActiveAt.Before(*b.LastActiveAt)
		if older == newest {
			return 1
		}
		return -1
	})
}
