package lang

// NormalizeNumbers converts integer-valued float64 entries to int64. JSON
// unmarshal into map[string]any leaves every number as float64; templates
// render large float64 as scientific notation (e.g. 3.052179e+06), which
// breaks scripts that compare the rendered value as a string (`pid` etc.).
// Walks nested maps and slices so deep outputs are covered too.
func NormalizeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = NormalizeNumbers(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = NormalizeNumbers(vv)
		}
		return out
	}
	return v
}

// NormalizeOutputs applies NormalizeNumbers to a whole outputs map, the
// shape every action's parsed stdout and every surface's `self`/`prev`/
// `workflow.outputs` root take before a value's `{ from = ... }` projects
// out of them.
func NormalizeOutputs(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out, _ := NormalizeNumbers(m).(map[string]any)
	return out
}
