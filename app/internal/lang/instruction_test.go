package lang

import "testing"

func TestRenderInstruction(t *testing.T) {
	env := Environment{
		"resource": map[string]any{
			"id":    "https://example.test/r/1",
			"state": map[string]any{"revision": "sha2", "checks_count": 3},
		},
		"inputs": map[string]any{"instruction": "be brief"},
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a projection in prose position resolves",
			body: "Review {{ resource.id }} now.",
			want: "Review https://example.test/r/1 now.",
		},
		{
			name: "a nested state key resolves",
			body: "At revision {{ resource.state.revision }}.",
			want: "At revision sha2.",
		},
		{
			name: "a non-string value is stringified, because prose has nowhere to put a type",
			body: "{{ resource.state.checks_count }} checks.",
			want: "3 checks.",
		},
		{
			name: "spacing inside the delimiters is not significant",
			body: "{{resource.id}} and {{   inputs.instruction   }}",
			want: "https://example.test/r/1 and be brief",
		},
		{
			name: "prose with no projection is returned as it is",
			body: "Just do the work.",
			want: "Just do the work.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderInstruction(tt.body, env)
			if err != nil {
				t.Fatalf("RenderInstruction: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Absence is explicit everywhere in the language, and prose is no exception:
// a projection that resolves to nothing fails rather than rendering an empty
// space where a fact was supposed to be.
func TestRenderInstruction_AbsentProjectionFails(t *testing.T) {
	_, err := RenderInstruction("At {{ resource.state.revision }}.", Environment{
		"resource": map[string]any{"state": map[string]any{}},
	})
	if err == nil {
		t.Fatal("expected an error for a projection that resolved to nothing")
	}
}
