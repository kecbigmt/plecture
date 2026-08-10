//go:build integration

package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
)

// Acceptance: the real service stack (state.Store + service.List), driven
// through the HTTP handler, surfaces a session that exists in state.json.
//
// Given a session persisted in a temp state store,
// When GET / is served by the live service,
// Then the response lists that session.
func TestAcceptance_SessionAppearsInList(t *testing.T) {
	store := state.NewStore(t.TempDir())
	sess := &domain.Session{
		Name:         "acceptance/web-1",
		URL:          "https://github.com/acceptance/web/issues/1",
		URLType:      "issue",
		OwnerRepo:    "acceptance/web",
		Number:       1,
		Branch:       "issue/1",
		WorktreePath: "/nonexistent/worktree",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.Put(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	svc := newLiveService(config.Load(), store)
	rec := get(t, svc, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "acceptance/web-1") {
		t.Errorf("seeded session not listed; body:\n%s", body)
	}
}

// Acceptance: the real service stack, driven through the HTTP handler, renders
// the detail page for a session that exists in state.json.
//
// Given a session persisted in a temp state store,
// When GET /sessions/<name> is served by the live service,
// Then the response shows that session's branch and a diagnostic for the
// missing worktree (the seeded path does not exist).
func TestAcceptance_SessionDetail(t *testing.T) {
	store := state.NewStore(t.TempDir())
	sess := &domain.Session{
		Name:         "acceptance/web-2",
		URL:          "https://github.com/acceptance/web/issues/2",
		URLType:      "issue",
		OwnerRepo:    "acceptance/web",
		Number:       2,
		Branch:       "issue/2",
		WorktreePath: "/nonexistent/worktree",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.Put(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	svc := newLiveService(config.Load(), store)
	rec := get(t, svc, "/sessions/acceptance/web-2")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "acceptance/web-2") || !strings.Contains(body, "issue/2") {
		t.Errorf("detail missing session fields; body:\n%s", body)
	}
	if !strings.Contains(body, "worktree is missing") {
		t.Errorf("detail should surface the missing-worktree diagnostic; body:\n%s", body)
	}
}

// Acceptance: an unknown session name returns 404 through the real stack.
func TestAcceptance_SessionDetailNotFound(t *testing.T) {
	store := state.NewStore(t.TempDir())
	svc := newLiveService(config.Load(), store)
	rec := get(t, svc, "/sessions/acceptance/missing-1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// acceptancePost drives a same-origin htmx POST through a handler built from the
// real service stack.
func acceptancePost(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set(csrfHeaderName, "tok")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Acceptance: a create for a repo outside the allowlist is a 403 through the
// real stack — the allowlist check fires before any git work.
func TestAcceptance_CreateRepoNotAllowed(t *testing.T) {
	store := state.NewStore(t.TempDir())
	cfg := config.Load()
	cfg.RepoAllowlist = []string{"only/allowed"}

	h := New(newLiveService(cfg, store)).Routes()
	rec := acceptancePost(t, h, "/sessions", url.Values{
		"url": {"https://github.com/other/repo/issues/1"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body:\n%s", rec.Code, rec.Body.String())
	}
}
