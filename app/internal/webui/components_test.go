package webui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/plecture/plect/app/internal/domain"
	"github.com/plecture/plect/app/internal/service"
)

// exec runs a named component partial with data and returns its HTML.
func exec(t *testing.T, name string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("execute %q: %v", name, err)
	}
	return buf.String()
}

// badge renders the status as visible text in a rounded-full pill, colored by
// statusClass.
func TestBadge_RendersStatus(t *testing.T) {
	out := exec(t, "badge", domain.RunUp)
	if !strings.Contains(out, ">up<") {
		t.Errorf("badge missing status text: %s", out)
	}
	if !strings.Contains(out, "rounded-full") {
		t.Errorf("badge not pill-shaped: %s", out)
	}
	if !strings.Contains(out, statusClass(domain.RunUp)) {
		t.Errorf("badge missing status color classes: %s", out)
	}
}

// button: variant drives the class string, label is the text, type defaults to
// "button", and name/value are emitted when provided.
func TestButton_VariantAndAttrs(t *testing.T) {
	out := exec(t, "button", map[string]any{
		"variant": "destructive", "label": "Destroy", "name": "action", "value": "destroy",
	})
	for _, want := range []string{
		`type="button"`, `name="action"`, `value="destroy"`, ">Destroy<",
		"bg-destructive", "focus-visible:ring-ring",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("button missing %q: %s", want, out)
		}
	}

	// An explicit type overrides the default.
	if out := exec(t, "button", map[string]any{"type": "submit", "label": "Go"}); !strings.Contains(out, `type="submit"`) {
		t.Errorf("button did not honor explicit type: %s", out)
	}
}

// input: the label is associated to the control via matching for/id, and the
// field carries a focus-visible ring (a11y baseline).
func TestInput_LabelAssociatedAndFocusRing(t *testing.T) {
	out := exec(t, "input", map[string]any{
		"id": "branch", "name": "branch", "label": "Branch", "required": true,
	})
	if !strings.Contains(out, `for="branch"`) || !strings.Contains(out, `id="branch"`) {
		t.Errorf("label not associated with input via for/id: %s", out)
	}
	if !strings.Contains(out, "aria-required=\"true\"") || !strings.Contains(out, " required") {
		t.Errorf("required input missing required/aria-required: %s", out)
	}
	if !strings.Contains(out, "focus-visible:ring-ring") {
		t.Errorf("input missing focus-visible ring: %s", out)
	}
}

// card renders one session as a list item with status, branch and github status,
// keeps depth via border (no shadow), and links the name to its detail page
// (names contain "/", so the href is the path-escaped /sessions/<name> form).
func TestCard_SessionRow(t *testing.T) {
	out := exec(t, "card", cardView{ListEntry: service.ListEntry{
		SessionName: "owner/repo-1", Title: "fix things",
		Run: domain.RunUp, Health: domain.HealthHealthy, Branch: "issue/1", DisplayStatus: "open",
	}})
	for _, want := range []string{
		`data-run="up"`, `data-health="healthy"`, "owner/repo-1", "fix things", "issue/1", "open",
		"border-border", "bg-card", `href="/sessions/owner/repo-1"`,
		// clicking opens the session in the right pane without a full navigation
		`hx-get="/sessions/owner/repo-1"`, `hx-target="#detail-pane"`, `hx-push-url="true"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "shadow") {
		t.Errorf("card should use border, not shadow: %s", out)
	}
}

// Only the open session row is marked current (the row visual is driven off
// aria-current). Behavior: selecting a session highlights exactly that row.
func TestCard_ActiveHighlighted(t *testing.T) {
	active := exec(t, "card", cardView{
		ListEntry: service.ListEntry{SessionName: "owner/repo-1", Run: domain.RunUp, Health: domain.HealthHealthy},
		Active:    true,
	})
	if !strings.Contains(active, `aria-current="page"`) {
		t.Errorf("open card missing aria-current: %s", active)
	}
	inactive := exec(t, "card", cardView{
		ListEntry: service.ListEntry{SessionName: "owner/repo-2", Run: domain.RunUp, Health: domain.HealthHealthy},
	})
	// Match the attribute, not the substring: the row's class carries
	// has-[[aria-current=page]]:… which mentions aria-current unconditionally.
	if strings.Contains(inactive, `aria-current="page"`) {
		t.Errorf("non-open card should not be marked current: %s", inactive)
	}
}

// dialog: native <dialog>, labelled by its title, with a method="dialog" form so
// the browser handles Esc / focus trap / focus restore. Confirm defaults to the
// destructive variant.
func TestDialog_NativeAndLabelled(t *testing.T) {
	out := exec(t, "dialog", map[string]any{
		"id": "destroy", "title": "Destroy session?", "message": "This cannot be undone.",
		"confirmLabel": "Destroy",
	})
	for _, want := range []string{
		"<dialog", `id="destroy"`, `aria-labelledby="destroy-title"`,
		`id="destroy-title"`, "Destroy session?", "This cannot be undone.",
		`method="dialog"`, ">Cancel<", ">Destroy<", "bg-destructive",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dialog missing %q: %s", want, out)
		}
	}
}

// dialogBackdropClose matches a <dialog> whose click handler closes the dialog
// based on the event target — i.e. clicking the backdrop (not the content)
// dismisses it. We assert the contract loosely (a target-aware close handler is
// present) rather than the exact JS; whether only the backdrop closes it is
// browser-verified.
var dialogBackdropClose = regexp.MustCompile(`<dialog\b[^>]*\bonclick="[^"]*\.close\(\)`)

// Behavior: clicking outside the dialog dismisses it.
func TestDialog_ClosesOnBackdropClick(t *testing.T) {
	out := exec(t, "dialog", map[string]any{"id": "d", "title": "t"})
	if !dialogBackdropClose.MatchString(out) {
		t.Errorf("dialog has no backdrop-close handler: %s", out)
	}
	if !strings.Contains(out, "event.target") {
		t.Errorf("close handler should discriminate backdrop vs content via the event target: %s", out)
	}
}

// Behavior: a modal dialog appears centered. Tailwind preflight resets margin:0
// on every element, overriding the UA stylesheet's margin:auto and pinning a
// showModal() dialog to the top-left. This guards that the centering rule stays
// in the generated CSS; the visual result itself is browser-verified.
func TestCSS_CentersDialog(t *testing.T) {
	css, err := assetsFS.ReadFile("assets/static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	norm := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(css))
	if !strings.Contains(norm, "dialog{margin:auto}") {
		t.Error("app.css does not center <dialog> (want `dialog { margin: auto }`); preflight will pin it top-left")
	}
}

// buttonClass returns a distinct, non-empty class string per variant, and an
// unknown variant falls back to the default (not empty / not a bare base).
func TestButtonClass_DistinctPerVariant(t *testing.T) {
	variants := []string{"default", "destructive", "outline", "ghost"}
	seen := map[string]string{}
	for _, v := range variants {
		cls := buttonClass(v)
		if cls == "" {
			t.Errorf("buttonClass(%q) is empty", v)
		}
		if prev, dup := seen[cls]; dup {
			t.Errorf("buttonClass(%q) == buttonClass(%q); not distinct", v, prev)
		}
		seen[cls] = v
	}
	if buttonClass("bogus") != buttonClass("default") {
		t.Error("unknown variant should fall back to default")
	}
}

// isWebURL only treats http(s) URLs as navigable; resolver-less ids and other
// schemes (which the detail view must render as plain text) are not.
func TestIsWebURL(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"https://github.com/owner/repo/issues/7", true},
		{"http://example.com", true},
		{"my-experiment", false},
		{"owner:foo", false},
		{"", false},
		{"ftp://example.com/x", false},
		{"javascript:alert(1)", false},
	} {
		if got := isWebURL(c.in); got != c.want {
			t.Errorf("isWebURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
