package commands

import (
	"reflect"
	"testing"
)

func TestParseTemplateVars(t *testing.T) {
	got, err := parseTemplateVars([]string{"a=1", "b=x=y"})
	if err != nil {
		t.Fatalf("parseTemplateVars() error: %v", err)
	}
	want := map[string]any{"a": "1", "b": "x=y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTemplateVars() = %v, want %v", got, want)
	}

	for _, bad := range []string{"noeq", "=novalue"} {
		if _, err := parseTemplateVars([]string{bad}); err == nil {
			t.Errorf("parseTemplateVars(%q) expected error, got nil", bad)
		}
	}
}
