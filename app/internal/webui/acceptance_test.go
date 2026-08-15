//go:build integration

package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// mountResolverOnlyProvider registers a minimal global-layer workflow +
// provider so dispatch can resolve a github.com URL without depending on
// whatever catalogs.toml (if any) happens to be registered on the machine
// the test runs on. The provider's setup hook is a placeholder that must
// never actually run — Create's allowlist check is meant to reject the
// request before any provider hook does.
func mountResolverOnlyProvider(t *testing.T, cfg *config.Config) {
	t.Helper()
	base := t.TempDir()
	cfg.BaseDir = base
	if err := os.MkdirAll(filepath.Join(base, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	workflowTOML := "provider = \"test-gh\"\n"
	if err := os.WriteFile(filepath.Join(base, "workflows", "test.toml"), []byte(workflowTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	providerTOML := "match = '^https://github\\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/(?:issues|pull)/(?P<number>\\d+)'\n" +
		"name  = \"{{.owner}}/{{.repo}}-{{.number}}\"\n" +
		// Never invoked (the allowlist check rejects the request first) —
		// only present because LoadProviders requires a non-empty setup.
		"setup = \"exit 1\"\n"
	if err := os.WriteFile(filepath.Join(base, "providers", "test-gh.toml"), []byte(providerTOML), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: the real service stack (state.Store + service.List), driven
// through the HTTP handler, surfaces a session that exists in state.json.
//
// Given a session persisted in a temp state store,
// When GET / is served by the live service,
// Then the response lists that session.
func TestAcceptance_SessionAppearsInList(t *testing.T) {
	store := state.NewStore(t.TempDir())
	sess := &domain.Session{
		Name:        "acceptance/web-1",
		ResourceID:  "https://github.com/acceptance/web/issues/1",
		Branch:      "issue/1",
		WorkdirPath: "/nonexistent/workdir",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.Put(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	svc := newLiveService(cfg, store)
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
// missing workdir (the seeded path does not exist).
func TestAcceptance_SessionDetail(t *testing.T) {
	store := state.NewStore(t.TempDir())
	sess := &domain.Session{
		Name:        "acceptance/web-2",
		ResourceID:  "https://github.com/acceptance/web/issues/2",
		Branch:      "issue/2",
		WorkdirPath: "/nonexistent/workdir",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.Put(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	svc := newLiveService(cfg, store)
	rec := get(t, svc, "/sessions/acceptance/web-2")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "acceptance/web-2") || !strings.Contains(body, "issue/2") {
		t.Errorf("detail missing session fields; body:\n%s", body)
	}
	if !strings.Contains(body, "(missing)") {
		t.Errorf("detail should surface the missing-workdir diagnostic; body:\n%s", body)
	}
}

// Acceptance: an unknown session name returns 404 through the real stack.
func TestAcceptance_SessionDetailNotFound(t *testing.T) {
	store := state.NewStore(t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	svc := newLiveService(cfg, store)
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

// Acceptance: a create for a resource outside the allowlist is a 403 through
// the real stack — the allowlist check fires before any provider work.
func TestAcceptance_CreateResourceNotAllowed(t *testing.T) {
	store := state.NewStore(t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	mountResolverOnlyProvider(t, cfg)
	cfg.ResourceAllowlist = []string{`^https://github\.com/only/allowed/`}

	h := New(newLiveService(cfg, store)).Routes()
	rec := acceptancePost(t, h, "/sessions", url.Values{
		"url": {"https://github.com/other/repo/issues/1"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body:\n%s", rec.Code, rec.Body.String())
	}
}
