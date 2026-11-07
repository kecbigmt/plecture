package dispatch

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
)

func terminalRevisionSession(t *testing.T, cfg *config.Config) *domain.Session {
	t.Helper()
	revision, err := cfg.CaptureWorkflowRevision("", "coding")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	return &domain.Session{Name: "o/r-1", Workflow: "coding", WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
}

func TestResolveTerminalOwner_FindsDeclaringNode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), `
[tmux]
kind  = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo '{}'"

[tmux.health.alive]
type = "noop"

[tmux.terminal.send_text]
type    = "exec"
command = "tmux"
args    = ["send-keys", "-t", { from = "self.outputs.session_name" }, "--"]
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"
[[coding.nodes]]
uses = "tmux"
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
	s := terminalRevisionSession(t, cfg)

	owner := resolveTerminalOwner(slog.Default(), cfg, s, wf)
	if owner == nil || owner.NodeID != "tmux" {
		t.Fatalf("owner = %+v, want the tmux node", owner)
	}
	if _, err := owner.Ops.Verb("send_text"); err != nil {
		t.Fatalf("send_text: %v", err)
	}
}

func TestResolveTerminalOwner_NilWhenNoNodeDeclaresTerminal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "tasks", "envfile.toml"), `
[envfile]
kind  = "effect"
scope = "session"

[envfile.setup]
type   = "shell"
script = "echo '{}'"

[envfile.health.alive]
type = "noop"
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"
[[coding.nodes]]
uses = "envfile"
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
	s := terminalRevisionSession(t, cfg)

	if owner := resolveTerminalOwner(slog.Default(), cfg, s, wf); owner != nil {
		t.Fatalf("owner = %+v, want none when no effect declares an interactive endpoint", owner)
	}
}

// TestResolveTerminalOwner_CompileFailureIsNonFatal guards the tolerant
// contract documented on resolveTerminalOwner: a broken plan (a workflow
// node the config no longer has a declaration for) must not stop the
// dispatcher from building — it only means the terminal capability is
// unavailable, logged rather than propagated.
func TestResolveTerminalOwner_CompileFailureIsNonFatal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"
[[coding.nodes]]
uses = "missing"
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
	if owner := resolveTerminalOwner(logger, cfg, s, wf); owner != nil {
		t.Fatalf("owner = %+v, want none on compile failure", owner)
	}
	if !bytes.Contains(logs.Bytes(), []byte("compile plan failed")) {
		t.Errorf("expected a compile-failure warning to be logged, got %q", logs.String())
	}
}

func TestResolveTerminalOwner_WithheldOnceTheSessionsProjectRootIsRevoked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		revoke bool
	}{
		{"trusted root", false},
		{"revoked root", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "widgets")
			writeFile(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
			writeFile(t, filepath.Join(root, ".plect", "tasks", "pane.toml"), `
[pane]
kind  = "effect"
scope = "run"

[pane.setup]
type   = "shell"
script = "echo '{}'"

[pane.health.alive]
type = "noop"

[pane.terminal.send_text]
type   = "shell"
script = 'echo "$1"'
`)
			writeFile(t, filepath.Join(root, ".plect", "workflows", "coding.toml"), "[coding]\nkind = \"workflow\"\nresource = \"issue\"\n[[coding.nodes]]\nuses = \"pane\"\n")
			cfg := &config.Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}
			workflows, err := cfg.LoadWorkflows(root)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := cfg.CaptureWorkflowRevision(root, "coding")
			if err != nil {
				t.Fatal(err)
			}
			body, digest, err := revision.CanonicalBody()
			if err != nil {
				t.Fatal(err)
			}
			s := &domain.Session{Name: "review-1", Workflow: "coding", ProjectRoot: root, WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
			if tc.revoke {
				cfg.TrustedProjectRoots = nil
			}

			owner := resolveTerminalOwner(slog.Default(), cfg, s, workflows["coding"])
			if (owner != nil) == tc.revoke {
				t.Fatalf("owner = %+v, want a terminal only while the root is trusted", owner)
			}
		})
	}
}

func TestTerminalResolver_RechecksTheProjectRootAtEachDelivery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		revoke func(t *testing.T, cfg *config.Config, root string)
	}{
		{"trust removed after the dispatcher was built", func(t *testing.T, cfg *config.Config, root string) { cfg.TrustedProjectRoots = nil }},
		{"marker deleted after the dispatcher was built", func(t *testing.T, cfg *config.Config, root string) {
			if err := os.Remove(filepath.Join(root, ".plect", "project.toml")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "widgets")
			writeFile(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
			writeFile(t, filepath.Join(root, ".plect", "tasks", "pane.toml"), `
[pane]
kind  = "effect"
scope = "run"

[pane.setup]
type   = "shell"
script = "echo '{}'"

[pane.health.alive]
type = "noop"

[pane.terminal.send_text]
type   = "shell"
script = 'echo project-terminal'
`)
			writeFile(t, filepath.Join(root, ".plect", "workflows", "coding.toml"), "[coding]\nkind = \"workflow\"\nresource = \"issue\"\n[[coding.nodes]]\nuses = \"pane\"\n")
			cfg := &config.Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}
			workflows, err := cfg.LoadWorkflows(root)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := cfg.CaptureWorkflowRevision(root, "coding")
			if err != nil {
				t.Fatal(err)
			}
			body, digest, err := revision.CanonicalBody()
			if err != nil {
				t.Fatal(err)
			}
			s := &domain.Session{Name: "review-1", Workflow: "coding", ProjectRoot: root, WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
			owner := resolveTerminalOwner(slog.Default(), cfg, s, workflows["coding"])
			if owner == nil {
				t.Fatal("no terminal owner while the root is trusted")
			}
			resolve := terminalResolver(s, owner, task.SessionVars{Name: s.Name}, func() *config.Config { return cfg })
			if _, err := resolve("send_text", t.TempDir()); err != nil {
				t.Fatalf("send_text while trusted: %v", err)
			}
			tc.revoke(t, cfg, root)

			if command, err := resolve("send_text", t.TempDir()); err == nil || !strings.Contains(err.Error(), root) {
				t.Fatalf("send_text after revocation = %q, %v; want it refused naming %s", command, err, root)
			}
		})
	}
}
