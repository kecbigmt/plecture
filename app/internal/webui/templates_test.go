package webui

import "testing"

// Templates parse at init and define every block the handlers render.
func TestTemplatesDefined(t *testing.T) {
	if templates == nil {
		t.Fatal("templates not parsed")
	}
	for _, name := range []string{
		"list", "rows", "error-banner", "detail-pane", "detail-empty", "detail-notfound-pane",
		"badge", "button", "card", "input", "dialog",
	} {
		if templates.Lookup(name) == nil {
			t.Errorf("template %q not defined", name)
		}
	}
}
