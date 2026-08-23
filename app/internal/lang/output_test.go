package lang

import "testing"

func TestParseOutputs(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  map[string]any
		isErr bool
	}{
		{"empty", "", map[string]any{}, false},
		{"whitespace", "  \n  ", map[string]any{}, false},
		{"object", `{"pid":123}`, map[string]any{"pid": float64(123)}, false},
		{"invalid", `not json`, nil, true},
		{"array", `[1,2]`, nil, true},
		{"null", `null`, nil, true},
		{"null_with_whitespace", "  null  ", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseOutputs([]byte(c.in))
			if c.isErr {
				if err == nil {
					t.Fatalf("expected error, got nil (output=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("output length mismatch: got %v want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("key %q: got %v want %v", k, got[k], v)
				}
			}
		})
	}
}
