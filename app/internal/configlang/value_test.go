package configlang

import (
	"errors"
	"testing"
)

func wantDiag(t *testing.T, err error, code Code, layer Layer) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got no error", code)
	}
	var d *Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("want %s, got a plain error: %v", code, err)
	}
	if d.Code != code {
		t.Fatalf("want %s, got %s (%v)", code, d.Code, err)
	}
	if d.Layer != layer {
		t.Errorf("%s: want layer %s, got %s", code, layer, d.Layer)
	}
}

func TestParseValueLiteralTakesNoWrapper(t *testing.T) {
	for _, raw := range []any{"deny-write", int64(3), 1.5, true} {
		v, err := ParseValue(raw, ClassData, Position{})
		if err != nil {
			t.Fatalf("%v: %v", raw, err)
		}
		if v.Form != FormLiteral {
			t.Errorf("%v: want form %s, got %s", raw, FormLiteral, v.Form)
		}
		if v.Literal != raw {
			t.Errorf("%v: literal round-trip lost the value: %v", raw, v.Literal)
		}
	}
}

func TestParseValueForms(t *testing.T) {
	tests := []struct {
		name  string
		raw   map[string]any
		check func(*testing.T, *Value)
	}{
		{
			name: "required projection",
			raw:  map[string]any{"from": "inputs.owner"},
			check: func(t *testing.T, v *Value) {
				if v.Form != FormFrom || v.From != "inputs.owner" {
					t.Fatalf("got %+v", v)
				}
				if v.HasDefault || v.Optional {
					t.Errorf("a bare projection declares neither default nor optional: %+v", v)
				}
			},
		},
		{
			name: "projection with default",
			raw:  map[string]any{"from": "inputs.assignees", "default": ""},
			check: func(t *testing.T, v *Value) {
				if !v.HasDefault || v.Default != "" {
					t.Fatalf("got %+v", v)
				}
			},
		},
		{
			name: "optional projection",
			raw:  map[string]any{"from": "event.metadata.url", "optional": true},
			check: func(t *testing.T, v *Value) {
				if !v.Optional {
					t.Fatalf("got %+v", v)
				}
			},
		},
		{
			name: "computation",
			raw:  map[string]any{"expr": "event.body != '' ? event.body : event.summary"},
			check: func(t *testing.T, v *Value) {
				if v.Form != FormExpr || v.Expr == "" {
					t.Fatalf("got %+v", v)
				}
			},
		},
		{
			name: "json serialization",
			raw:  map[string]any{"json": map[string]any{"from": "event"}},
			check: func(t *testing.T, v *Value) {
				if v.Form != FormJSON || v.JSON == nil || v.JSON.Leaf == nil || v.JSON.Leaf.From != "event" {
					t.Fatalf("got %+v", v)
				}
			},
		},
		{
			name: "json object operand",
			raw: map[string]any{"json": map[string]any{
				"text":    map[string]any{"expr": "event.summary"},
				"channel": "C123",
			}},
			check: func(t *testing.T, v *Value) {
				if v.JSON.Object == nil {
					t.Fatalf("got %+v", v.JSON)
				}
				if v.JSON.Object["text"].Leaf.Form != FormExpr {
					t.Errorf("text leaf: got %+v", v.JSON.Object["text"])
				}
				if v.JSON.Object["channel"].Leaf.Literal != "C123" {
					t.Errorf("channel leaf: got %+v", v.JSON.Object["channel"])
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ParseValue(tc.raw, ClassData, Position{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, v)
		})
	}
}

func TestParseValueCapabilityTags(t *testing.T) {
	terminal := map[string]any{"terminal": "send_text"}
	bin := map[string]any{"bin": "codex-agent-activity"}

	for _, raw := range []map[string]any{terminal, bin} {
		v, err := ParseValue(raw, ClassBinding, Position{})
		if err != nil {
			t.Fatalf("%v: %v", raw, err)
		}
		if v.Form != FormTerminal && v.Form != FormBin {
			t.Errorf("%v: got form %s", raw, v.Form)
		}
	}
	for _, raw := range []map[string]any{terminal, bin} {
		_, err := ParseValue(raw, ClassData, Position{})
		wantDiag(t, err, CodeValueTagSurface, LayerStructural)
	}
}

func TestParseValueRejections(t *testing.T) {
	tests := []struct {
		name  string
		raw   any
		class ValueClass
		code  Code
	}{
		{"from and expr", map[string]any{"from": "inputs.owner", "expr": "inputs.owner"}, ClassData, CodeValueFromAndExpr},
		{"default and optional", map[string]any{"from": "inputs.assignees", "default": "", "optional": true}, ClassData, CodeValueDefaultAndOptional},
		{"unknown tag key", map[string]any{"from": "inputs.owner", "fallback": "unknown"}, ClassData, CodeValueTagUnknown},
		{"expr with a companion key", map[string]any{"expr": "inputs.owner", "default": ""}, ClassData, CodeValueTagUnknown},
		{"no discriminating key", map[string]any{"type": "string"}, ClassData, CodeValueTagUnknown},
		{"two discriminating keys", map[string]any{"from": "inputs.x", "terminal": "send_text"}, ClassBinding, CodeValueTagUnknown},
		{"terminal verb outside the vocabulary", map[string]any{"terminal": "send_signal"}, ClassBinding, CodeValueTagUnknown},
		{"a tagged value where only literal data is accepted", map[string]any{"from": "nodes.pane.outputs.session_name"}, ClassLiteral, CodeValueTagSurface},
		{"a capability tag inside a json operand", map[string]any{"json": map[string]any{"x": map[string]any{"terminal": "send_text"}}}, ClassBinding, CodeValueTagSurface},
		{"a nested json operand", map[string]any{"json": map[string]any{"x": map[string]any{"json": "y"}}}, ClassBinding, CodeValueTagUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseValue(tc.raw, tc.class, Position{})
			wantDiag(t, err, tc.code, LayerStructural)
		})
	}
}

func TestCheckNoTaggedValuesInContract(t *testing.T) {
	contract := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sender": map[string]any{"terminal": "send_text"},
		},
	}
	wantDiag(t, checkNoTaggedValues(contract, Position{Path: "runtime.inputs_schema"}), CodeValueTagSurface, LayerStructural)

	clean := map[string]any{
		"type":       "object",
		"required":   []any{"owner"},
		"properties": map[string]any{"owner": map[string]any{"type": "string"}},
	}
	if err := checkNoTaggedValues(clean, Position{}); err != nil {
		t.Errorf("a plain JSON Schema document carries no tagged value: %v", err)
	}
}
