package dispatch

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
)

func TestResolveTerminalOwner_FindsDeclaringNode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), `
scope = "run"
setup = "echo '{}'"

[terminal]
attach     = "tmux attach -t {{.Self.session_name}}"
capture    = "tmux capture-pane -p -t {{.Self.session_name}}"
send_text  = "tmux send-keys -t {{.Self.session_name}} -- \"$1\""
send_keys  = "tmux send-keys -t {{.Self.session_name}} \"$1\""
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[[nodes]]
id = "tmux"
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	wf := workflows["coding"]
	s := &domain.Session{Name: "o/r-1"}

	nodeID, ops, _ := resolveTerminalOwner(slog.Default(), cfg, s, wf)
	if nodeID != "tmux" {
		t.Fatalf("nodeID = %q, want %q", nodeID, "tmux")
	}
	if ops == nil || ops.SendText == "" {
		t.Fatalf("ops = %+v, want a populated [terminal] table", ops)
	}
}

func TestResolveTerminalOwner_NilWhenNoNodeDeclaresTerminal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeFile(t, filepath.Join(globalDir, "tasks", "envfile.toml"), `
scope = "session"
setup = "echo '{}'"
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[[nodes]]
id = "envfile"
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	wf := workflows["coding"]
	s := &domain.Session{Name: "o/r-1"}

	nodeID, ops, _ := resolveTerminalOwner(slog.Default(), cfg, s, wf)
	if nodeID != "" || ops != nil {
		t.Fatalf("nodeID/ops = %q/%+v, want empty when no node declares [terminal]", nodeID, ops)
	}
}

// TestResolveTerminalOwner_CompileFailureIsNonFatal guards the tolerant
// contract documented on resolveTerminalOwner: a broken task graph (a
// workflow node the config no longer has a definition for) must not stop
// the dispatcher from building — it only means {{terminal "..."}} is
// unavailable, logged rather than propagated.
func TestResolveTerminalOwner_CompileFailureIsNonFatal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "")
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[[nodes]]
id = "missing"
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	wf := workflows["coding"]
	s := &domain.Session{Name: "o/r-1"}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	nodeID, ops, _ := resolveTerminalOwner(logger, cfg, s, wf)
	if nodeID != "" || ops != nil {
		t.Fatalf("nodeID/ops = %q/%+v, want empty on compile failure", nodeID, ops)
	}
	if !bytes.Contains(logs.Bytes(), []byte("compile plan failed")) {
		t.Errorf("expected a compile-failure warning to be logged, got %q", logs.String())
	}
}
