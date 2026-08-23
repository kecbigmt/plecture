package lang

import "testing"

func TestNormalizeOutputs_IntegerValuedFloatsBecomeInt64(t *testing.T) {
	got := NormalizeOutputs(map[string]any{
		"pid":     float64(3052179),
		"ratio":   1.5,
		"nested":  map[string]any{"count": float64(2)},
		"list":    []any{float64(1), 2.5},
		"literal": "x",
	})
	if got["pid"] != int64(3052179) {
		t.Errorf("pid = %v (%T), want int64(3052179)", got["pid"], got["pid"])
	}
	if got["ratio"] != 1.5 {
		t.Errorf("ratio = %v, want 1.5 unchanged", got["ratio"])
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok || nested["count"] != int64(2) {
		t.Errorf("nested.count = %v, want int64(2)", got["nested"])
	}
	list, ok := got["list"].([]any)
	if !ok || list[0] != int64(1) || list[1] != 2.5 {
		t.Errorf("list = %v, want [int64(1), 2.5]", got["list"])
	}
	if got["literal"] != "x" {
		t.Errorf("literal = %v, want unchanged", got["literal"])
	}
}

func TestNormalizeOutputs_NilStaysNil(t *testing.T) {
	if got := NormalizeOutputs(nil); got != nil {
		t.Errorf("NormalizeOutputs(nil) = %v, want nil", got)
	}
}
