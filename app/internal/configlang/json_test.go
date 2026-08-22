package configlang

import (
	"fmt"
	"testing"
)

// staticLeaves resolves a json operand's leaves from a fixed table, so a
// serialization test exercises the serializer rather than an evaluator.
func staticLeaves(projections map[string]any, computations map[string]any) LeafValue {
	return func(v *Value) (any, bool, error) {
		switch v.Form {
		case FormLiteral:
			return v.Literal, false, nil
		case FormFrom:
			got, ok := projections[v.From]
			if ok {
				return got, false, nil
			}
			if v.HasDefault {
				return v.Default, false, nil
			}
			if v.Optional {
				return nil, true, nil
			}
			return nil, false, fmt.Errorf("no source for %q", v.From)
		default:
			got, ok := computations[v.Expr]
			if !ok {
				return nil, false, fmt.Errorf("no value for %q", v.Expr)
			}
			return got, false, nil
		}
	}
}

func mustOperand(t *testing.T, raw map[string]any) *JSONOperand {
	t.Helper()
	v, err := ParseValue(raw, ClassData, Position{})
	if err != nil {
		t.Fatal(err)
	}
	return v.JSON
}

func TestRenderJSONIsDeterministic(t *testing.T) {
	operand := mustOperand(t, map[string]any{"json": map[string]any{
		"zulu":     map[string]any{"from": "inputs.zulu"},
		"alpha":    map[string]any{"from": "inputs.alpha"},
		"mike":     "literal",
		"kilo":     map[string]any{"expr": "event.summary"},
		"delta":    int64(7),
		"november": true,
		"echo":     map[string]any{"nested": map[string]any{"from": "inputs.alpha"}},
		"foxtrot":  []any{int64(3), map[string]any{"from": "inputs.zulu"}, "x"},
	}})
	leaves := staticLeaves(
		map[string]any{"inputs.zulu": "z", "inputs.alpha": "a"},
		map[string]any{"event.summary": "s"},
	)

	first, absent, err := RenderJSON(operand, leaves)
	if err != nil || absent {
		t.Fatalf("render: %v (absent=%v)", err, absent)
	}
	for i := 0; i < 64; i++ {
		again, _, err := RenderJSON(operand, leaves)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("serialization %d differs:\n  %s\n  %s", i, first, again)
		}
	}
	want := `{"alpha":"a","delta":7,"echo":{"nested":"a"},"foxtrot":[3,"z","x"],"kilo":"s","mike":"literal","november":true,"zulu":"z"}`
	if string(first) != want {
		t.Errorf("got  %s\nwant %s", first, want)
	}
}

func TestRenderJSONPropagatesAbsence(t *testing.T) {
	operand := mustOperand(t, map[string]any{"json": map[string]any{
		"url":  map[string]any{"from": "event.metadata.url", "optional": true},
		"type": map[string]any{"from": "event.type"},
	}})
	leaves := staticLeaves(map[string]any{"event.type": "user.emit"}, nil)
	got, _, err := RenderJSON(operand, leaves)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"user.emit"}`; string(got) != want {
		t.Errorf("an optional projection omits its key rather than supplying a sentinel: got %s, want %s", got, want)
	}
}

func TestRenderJSONWholeOperandAbsence(t *testing.T) {
	operand := mustOperand(t, map[string]any{"json": map[string]any{"from": "event", "optional": true}})
	data, absent, err := RenderJSON(operand, staticLeaves(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !absent || data != nil {
		t.Errorf("got %q (absent=%v), want no payload", data, absent)
	}
}

func TestRenderJSONDoesNotEscapeForHTML(t *testing.T) {
	operand := mustOperand(t, map[string]any{"json": map[string]any{"body": map[string]any{"from": "event.body"}}})
	got, _, err := RenderJSON(operand, staticLeaves(map[string]any{"event.body": "a < b && c > d"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	// The payload crosses a process boundary, not an HTML one.
	if want := `{"body":"a < b && c > d"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestRenderJSONReportsAMissingRequiredSource(t *testing.T) {
	operand := mustOperand(t, map[string]any{"json": map[string]any{"owner": map[string]any{"from": "inputs.owner"}}})
	if _, _, err := RenderJSON(operand, staticLeaves(nil, nil)); err == nil {
		t.Error("a missing source is an error unless the value declares default or optional")
	}
}
