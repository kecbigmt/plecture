package traceid

import (
	"strings"
	"testing"
)

func TestGenerate_Format(t *testing.T) {
	id := Generate()
	if !strings.HasPrefix(id, "tr_") {
		t.Errorf("expected prefix 'tr_', got %q", id)
	}
	if len(id) != 11 {
		t.Errorf("expected length 11, got %d (%q)", len(id), id)
	}
}

func TestGenerate_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := Generate()
		if seen[id] {
			t.Fatalf("duplicate trace_id: %s", id)
		}
		seen[id] = true
	}
}
