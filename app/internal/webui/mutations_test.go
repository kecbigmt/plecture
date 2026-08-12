package webui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

// postForm drives a same-origin htmx POST: it carries the matching CSRF cookie
// and header so the security middleware lets it through, leaving the handler
// under test as the thing being exercised.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
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

// 2. create: a valid form runs Up (auto-create→run) with the parsed params and
// redirects to the new session's detail page.
func TestCreate_Success(t *testing.T) {
	svc := &fakeService{upResult: &service.UpResult{SessionName: "owner/repo-1"}}
	rec := postForm(t, New(svc).Routes(), "/sessions", url.Values{
		"url":    {"https://github.com/owner/repo/issues/1"},
		"tag":    {"exp"},
		"inputs": {`{"a":1}`},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/sessions/owner/repo-1" {
		t.Errorf("HX-Redirect = %q, want /sessions/owner/repo-1", got)
	}
	if svc.gotUp == nil {
		t.Fatal("Up was not called")
	}
	if svc.gotUp.Identifier != "https://github.com/owner/repo/issues/1" || svc.gotUp.Tag != "exp" {
		t.Errorf("Up params = %+v", svc.gotUp)
	}
	if svc.gotUp.Inputs["a"] == nil {
		t.Errorf("inputs not forwarded: %+v", svc.gotUp.Inputs)
	}
}

// The create form is the only entry point for resolver-less identifiers, so
// workflow must reach UpParams exactly like the CLI/MCP surfaces — a
// resolver-less resource id fails auto-create without an explicit
// workflow.
func TestCreate_ForwardsWorkflow(t *testing.T) {
	svc := &fakeService{upResult: &service.UpResult{SessionName: "owner/repo-1"}}
	postForm(t, New(svc).Routes(), "/sessions", url.Values{
		"url":      {"https://github.com/owner/repo/issues/1"},
		"workflow": {"custom-workflow"},
	})
	if svc.gotUp == nil {
		t.Fatal("Up was not called")
	}
	if svc.gotUp.Workflow != "custom-workflow" {
		t.Errorf("Workflow = %q, want custom-workflow", svc.gotUp.Workflow)
	}
}

// create: --task is shorthand for inputs.task, merged in like the CLI/MCP
// surfaces.
func TestCreate_ForwardsTaskAsInputsShorthand(t *testing.T) {
	svc := &fakeService{upResult: &service.UpResult{SessionName: "owner/repo-1"}}
	postForm(t, New(svc).Routes(), "/sessions", url.Values{
		"url":  {"https://github.com/owner/repo/issues/1"},
		"task": {"review"},
	})
	if svc.gotUp == nil {
		t.Fatal("Up was not called")
	}
	if svc.gotUp.Inputs["task"] != "review" {
		t.Errorf("Inputs[task] = %v, want review", svc.gotUp.Inputs["task"])
	}
}

// create: a task value conflicting with inputs.task is a 400, not a silent
// pick of one value.
func TestCreate_TaskConflictsWithInputs(t *testing.T) {
	svc := &fakeService{upResult: &service.UpResult{SessionName: "owner/repo-1"}}
	rec := postForm(t, New(svc).Routes(), "/sessions", url.Values{
		"url":    {"https://github.com/owner/repo/issues/1"},
		"task":   {"review"},
		"inputs": {`{"task":"work"}`},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if svc.gotUp != nil {
		t.Error("Up should not run when task conflicts with inputs")
	}
}

// 2. create: missing URL is rejected before touching the service.
func TestCreate_MissingURL(t *testing.T) {
	svc := &fakeService{}
	rec := postForm(t, New(svc).Routes(), "/sessions", url.Values{"url": {"  "}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if svc.gotUp != nil {
		t.Error("Up should not be called when URL is missing")
	}
}

// 2. create: malformed inputs JSON is a 400 caught before the service runs.
func TestCreate_BadInputsJSON(t *testing.T) {
	svc := &fakeService{}
	rec := postForm(t, New(svc).Routes(), "/sessions", url.Values{
		"url":    {"https://github.com/owner/repo/issues/1"},
		"inputs": {"not json"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if svc.gotUp != nil {
		t.Error("Up should not run with invalid inputs")
	}
}

// 2. create: service error codes map to HTTP statuses with a banner.
func TestCreate_ErrorMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{service.ErrRepoNotAllowed, http.StatusForbidden},
		{service.ErrInvalidURL, http.StatusBadRequest},
		{service.ErrExecutionFailed, http.StatusInternalServerError},
	}
	for _, c := range cases {
		svc := &fakeService{upErr: &service.Error{Code: c.code, Message: "boom"}}
		rec := postForm(t, New(svc).Routes(), "/sessions", url.Values{
			"url": {"https://github.com/owner/repo/issues/1"},
		})
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.code, rec.Code, c.want)
		}
		if !strings.Contains(strings.ToLower(rec.Body.String()), "error") {
			t.Errorf("%s: error banner missing", c.code)
		}
	}
}

// 3. up: runs Up for the named session and redirects back.
func TestUp_Success(t *testing.T) {
	svc := &fakeService{}
	rec := postForm(t, New(svc).Routes(), "/sessions/up", url.Values{"name": {"owner/repo-1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/sessions/owner/repo-1" {
		t.Errorf("HX-Redirect = %q, want detail path fallback", got)
	}
	if svc.gotUp == nil || svc.gotUp.Identifier != "owner/repo-1" {
		t.Errorf("Up params = %+v", svc.gotUp)
	}
}

// 3. down: runs Down for the named session and redirects back.
func TestDown_Success(t *testing.T) {
	svc := &fakeService{}
	rec := postForm(t, New(svc).Routes(), "/sessions/down", url.Values{"name": {"owner/repo-1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if svc.gotDown == nil || svc.gotDown.Identifier != "owner/repo-1" {
		t.Errorf("Down params = %+v", svc.gotDown)
	}
}

// 3. up: an unknown session surfaces a 404.
func TestUp_NotFound(t *testing.T) {
	svc := &fakeService{upErr: &service.Error{Code: service.ErrWorkspaceNotFound, Message: "no entry"}}
	rec := postForm(t, New(svc).Routes(), "/sessions/up", url.Values{"name": {"owner/missing-1"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// 4. destroy: passes force/delete_branch through and redirects to the list.
func TestDestroy_Success(t *testing.T) {
	svc := &fakeService{}
	rec := postForm(t, New(svc).Routes(), "/sessions/destroy", url.Values{
		"name":          {"owner/repo-1"},
		"force":         {"1"},
		"delete_branch": {"1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/" {
		t.Errorf("HX-Redirect = %q, want /", got)
	}
	if svc.gotDestroy == nil || !svc.gotDestroy.Force || !svc.gotDestroy.DeleteBranch {
		t.Errorf("Destroy params = %+v", svc.gotDestroy)
	}
}

// 4. destroy: cleanup warnings are shown in place rather than redirected away.
func TestDestroy_WithWarnings(t *testing.T) {
	svc := &fakeService{destroyResult: &service.DestroyResult{
		SessionName:     "owner/repo-1",
		CleanupWarnings: []string{"task foo cleanup failed"},
	}}
	rec := postForm(t, New(svc).Routes(), "/sessions/destroy", url.Values{
		"name":  {"owner/repo-1"},
		"force": {"1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("HX-Redirect") != "" {
		t.Error("should not redirect when there are warnings to show")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "task foo cleanup failed") {
		t.Error("cleanup warning not rendered")
	}
	if !strings.Contains(body, `href="/"`) {
		t.Error("warnings view should link back to the list")
	}
}

// Rendering: the list page offers a create form posting to /sessions.
func TestList_RendersCreateForm(t *testing.T) {
	body := get(t, &fakeService{}, "/").Body.String()
	if !strings.Contains(body, `hx-post="/sessions"`) {
		t.Error("create form missing")
	}
	if !strings.Contains(body, `name="url"`) {
		t.Error("create form URL field missing")
	}
}

// Rendering: the detail page offers up/down/destroy controls.
func TestDetail_RendersActions(t *testing.T) {
	body := get(t, &fakeService{status: sampleShow()}, "/sessions/owner/repo-7").Body.String()
	for _, want := range []string{
		`hx-post="/sessions/up"`,
		`hx-post="/sessions/down"`,
		`hx-post="/sessions/destroy"`,
		`id="destroy-dialog"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail actions missing %q", want)
		}
	}
}

// destroyDialogBackdropClose matches the detail page's destroy <dialog> when it
// carries a target-aware close handler (clicking the backdrop dismisses it).
var destroyDialogBackdropClose = regexp.MustCompile(`<dialog\b[^>]*\bid="destroy-dialog"[^>]*\bonclick="[^"]*\.close\(\)`)

// Behavior: clicking outside the destroy confirmation closes it.
func TestDetail_DestroyDialogClosesOnBackdrop(t *testing.T) {
	body := get(t, &fakeService{status: sampleShow()}, "/sessions/owner/repo-7").Body.String()
	if !destroyDialogBackdropClose.MatchString(body) {
		t.Error("destroy dialog should close on backdrop click")
	}
}

// Behavior: a failed mutation shows an inline banner. The handler returns a
// mapped 4xx/5xx with the banner body; htmx only swaps that into the form's
// target when responseHandling opts those codes in, so guard the config is on
// both form-bearing pages.
func TestPages_RenderErrorResponsesAsBanners(t *testing.T) {
	for _, path := range []string{"/", "/sessions/owner/repo-7"} {
		body := get(t, &fakeService{status: sampleShow()}, path).Body.String()
		if !strings.Contains(body, `name="htmx-config"`) || !strings.Contains(body, "responseHandling") {
			t.Errorf("%s: missing htmx-config responseHandling; mapped error banners would not swap", path)
		}
	}
}
