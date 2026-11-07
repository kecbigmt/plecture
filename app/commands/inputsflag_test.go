package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveInputsFlags returns (nil, nil) when neither --inputs nor
// --inputs-file is set, so callers can distinguish "no inputs" from "empty
// object" (an empty object is a deliberate value, not an omission).
func TestResolveInputsFlags_NeitherFlagSetReturnsNilNil(t *testing.T) {
	got, err := resolveInputsFlags("", "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("inputs = %#v, want nil", got)
	}
}

func TestResolveInputsFlags_JSONFlagParsesObject(t *testing.T) {
	got, err := resolveInputsFlags(`{"task":"work","count":2}`, "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got["task"] != "work" || got["count"] != float64(2) {
		t.Errorf("inputs = %#v, want task=work count=2", got)
	}
}

func TestResolveInputsFlags_FileFlagReadsAndParsesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inputs.json")
	if err := os.WriteFile(path, []byte(`{"key":"value"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := resolveInputsFlags("", path)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got["key"] != "value" {
		t.Errorf("inputs = %#v, want key=value", got)
	}
}

// --inputs and --inputs-file are mutually exclusive: the CLI must reject
// both being set before touching the filesystem, and the error must name
// both flags so a user can tell which combination is invalid.
func TestResolveInputsFlags_BothFlagsSetIsAnError(t *testing.T) {
	_, err := resolveInputsFlags(`{"a":1}`, "somefile.json")
	if err == nil {
		t.Fatal("err = nil, want an error naming --inputs and --inputs-file")
	}
	if got := err.Error(); got != "--inputs and --inputs-file are mutually exclusive" {
		t.Errorf("err = %q, want the mutual-exclusivity message", got)
	}
}

func TestResolveInputsFlags_MissingFileIsAnError(t *testing.T) {
	_, err := resolveInputsFlags("", filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("err = nil, want a file-read error")
	}
}

func TestResolveInputsFlags_InvalidJSONIsAnError(t *testing.T) {
	_, err := resolveInputsFlags("not json", "")
	if err == nil {
		t.Fatal("err = nil, want a JSON-parse error")
	}
}

// The inputs payload must be a JSON object, not a bare JSON scalar or array
// — even though both unmarshal into map[string]any's zero value without
// error for a scalar, a scalar fails at the type level.
func TestResolveInputsFlags_NonObjectJSONIsAnError(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"array", `[1,2,3]`},
		{"string", `"hello"`},
		{"number", `42`},
		{"null", `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveInputsFlags(tt.json, "")
			if err == nil {
				t.Fatalf("err = nil for %q, want an error", tt.json)
			}
		})
	}
}

func TestResolveInputsFlags_EmptyObjectIsPreservedNotNil(t *testing.T) {
	got, err := resolveInputsFlags(`{}`, "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil {
		t.Error("inputs = nil, want a non-nil empty map to distinguish from \"no inputs\"")
	}
	if len(got) != 0 {
		t.Errorf("inputs = %#v, want empty", got)
	}
}
