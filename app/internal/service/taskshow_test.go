package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func writeTaskFile(t *testing.T, base, id, body string) {
	t.Helper()
	dir := filepath.Join(base, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTaskShow_ReportsNestingChainOutermostFirst covers the static nesting
// view: every layer of the chain, in the order setup runs them.
func TestTaskShow_ReportsNestingChainOutermostFirst(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "claude", `
scope = "session"
setup = "true"
`)
	writeTaskFile(t, base, "guarded", `
inner = "claude"
`)
	writeTaskFile(t, base, "team", `
inner = "guarded"
`)

	detail, err := TaskShow(&config.Config{BaseDir: base}, "", "team")
	if err != nil {
		t.Fatalf("TaskShow: %v", err)
	}
	if detail.Scope != "session" {
		t.Errorf("Scope = %q, want the innermost task's %q", detail.Scope, "session")
	}
	var got []string
	for _, layer := range detail.Nesting {
		got = append(got, layer.ID)
	}
	want := []string{"team", "guarded", "claude"}
	if len(got) != len(want) {
		t.Fatalf("Nesting = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Nesting = %v, want %v", got, want)
		}
	}
	if detail.Nesting[0].Inner != "claude" && detail.Nesting[0].Inner != "guarded" {
		t.Errorf("outermost layer inner = %q, want the reference as written", detail.Nesting[0].Inner)
	}
}

// TestTaskShow_PlainTaskReportsNoChain keeps the nesting view out of the way
// of the tasks that are not nested at all.
func TestTaskShow_PlainTaskReportsNoChain(t *testing.T) {
	base := t.TempDir()
	writeTaskFile(t, base, "claude", `
scope = "run"
setup = "true"
`)
	detail, err := TaskShow(&config.Config{BaseDir: base}, "", "claude")
	if err != nil {
		t.Fatalf("TaskShow: %v", err)
	}
	if len(detail.Nesting) != 0 {
		t.Errorf("Nesting = %v, want empty for a plain task", detail.Nesting)
	}
}

func TestTaskShow_UnknownTask(t *testing.T) {
	base := t.TempDir()
	if _, err := TaskShow(&config.Config{BaseDir: base}, "", "nope"); err == nil {
		t.Fatal("TaskShow: want an error for an unknown task id, got nil")
	}
}
