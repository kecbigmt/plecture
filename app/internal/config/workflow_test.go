package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// trustAncestors lists dir and every ancestor of it as a trusted project root, for
// tests that exercise how overlay layers compose rather than whether one is
// trusted.
func trustAncestors(c *Config, dir string) *Config {
	c.TrustedProjectRoots = cascadeCandidates(dir)
	return c
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWorkflows_RepoDir(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "coding_claude.toml"), `
[coding_claude]
kind = "workflow"
resource = "issue"
[[coding_claude.nodes]]
id = "tmux"
uses = "tmux"

[coding_claude.nodes.inputs]
session_name = { from = "session.name" }
`)

	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(repoDir, "cwd")).LoadWorkflows(filepath.Join(repoDir, "cwd"))
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf, ok := got["coding_claude"]
	if !ok {
		t.Fatalf("expected coding_claude in %+v", got)
	}
	if wf.ID != "coding_claude" {
		t.Errorf("wf.ID = %q, want coding_claude", wf.ID)
	}
	if len(wf.Nodes) != 1 || wf.Nodes[0].ID != "tmux" {
		t.Errorf("nodes = %+v, want one tmux node", wf.Nodes)
	}
	if wf.Nodes[0].Inputs["session_name"].From != "session.name" {
		t.Errorf("input mapping not preserved: %+v", wf.Nodes[0].Inputs)
	}
}

func TestLoadWorkflows_BlocksField(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "coding_claude.toml"), `
[coding_claude]
kind = "workflow"
resource = "issue"
[[coding_claude.nodes]]
uses   = "teardown"
blocks = ["tmux", "claude"]

[[coding_claude.nodes]]
uses = "tmux"
`)

	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(repoDir, "cwd")).LoadWorkflows(filepath.Join(repoDir, "cwd"))
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf := got["coding_claude"]
	if len(wf.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(wf.Nodes))
	}
	if wf.Nodes[0].Uses != "teardown" {
		t.Fatalf("first node uses = %q, want teardown", wf.Nodes[0].Uses)
	}
	want := []string{"tmux", "claude"}
	if got := wf.Nodes[0].Blocks; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("blocks = %v, want %v", got, want)
	}
}

func TestLoadWorkflows_CascadeAppendsNodes(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "global"
`)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "session_extra"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	wf := got["shared"]
	if len(wf.Nodes) != 2 {
		t.Fatalf("expected 2 merged nodes, got %+v", wf.Nodes)
	}
	if wf.Nodes[0].ID != "global" || wf.Nodes[1].ID != "session_extra" {
		t.Errorf("merge order wrong: %+v (want global → session_extra)", wf.Nodes)
	}
}

func TestLoadWorkflows_CascadeMultipleLayers(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "g"
`)
	orgDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org")
	repoDir := filepath.Join(orgDir, "repo")
	sessionDir := filepath.Join(repoDir, "session")
	writeFile(t, filepath.Join(orgDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "o"
`)
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "r"
`)
	writeFile(t, filepath.Join(sessionDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "s"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(sessionDir, "cwd")).LoadWorkflows(filepath.Join(sessionDir, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	wf := got["shared"]
	if len(wf.Nodes) != 4 {
		t.Fatalf("expected 4 merged nodes, got %d: %+v", len(wf.Nodes), wf.Nodes)
	}
	wantOrder := []string{"g", "o", "r", "s"}
	for i, want := range wantOrder {
		if wf.Nodes[i].ID != want {
			t.Errorf("nodes[%d] = %q, want %q (full: %+v)", i, wf.Nodes[i].ID, want, wf.Nodes)
		}
	}
}

func TestLoadWorkflows_CascadeRejectsDuplicateNodeID(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "tmux"
`)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "tmux"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err == nil {
		t.Fatal("expected error for duplicate node id across cascade layers")
	}
}

func TestLoadWorkflows_CascadeStopsAtHome(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeFile(t, filepath.Join(tmpHome, ".plect", "workflows", "leak.toml"), `
[leak]
kind = "workflow"
resource = "issue"
[[leak.nodes]]
uses = "should_not_load"
`)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["leak"]; ok {
		t.Fatalf("workflow under $HOME/.plect was loaded; cascade should stop before $HOME")
	}
}

func TestLoadWorkflows_DifferentFilenamesStayIndependent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workflows", "a.toml"), `
[a]
kind = "workflow"
resource = "issue"
[[a.nodes]]
uses = "a_node"
`)
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workflows", "b.toml"), `
[b]
kind = "workflow"
resource = "issue"
[[b.nodes]]
uses = "b_node"
`)
	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 independent workflows, got %d: %+v", len(got), got)
	}
}

// A deeper layer may set a field the shallower layer left unset, but an
// inputs contract it redeclares is rejected rather than combined: two
// contracts silently intersected is a workflow whose accepted inputs no
// single layer states.
func TestLoadWorkflows_CascadeRejectsARedeclaredInputsSchema(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "g"

[shared.inputs_schema]
type = "object"
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "s"

[shared.inputs_schema]
type = "object"
required = ["x"]
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err == nil || !strings.Contains(err.Error(), `field "inputs_schema" is set by a shallower layer`) {
		t.Fatalf("LoadWorkflows = %v, want a redeclaration error naming inputs_schema", err)
	}
}

// Identity is the definition table's name, so a filename is free-form and an
// id outside the grammar is what fails — a hyphen in an id would otherwise
// reach a node id, where a dotted path segment has to stay addressable.
func TestLoadWorkflows_InvalidIDRejected(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "-free-form-name.toml"), `
["coding-claude"]
kind = "workflow"
resource = "issue"
[["coding-claude".nodes]]
uses = "tmux"
`)
	cfg := &Config{}
	_, err := trustAncestors(cfg, filepath.Join(repoDir, "cwd")).LoadWorkflows(filepath.Join(repoDir, "cwd"))
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-ID-INVALID") {
		t.Fatalf("LoadWorkflows = %v, want an invalid-id diagnostic", err)
	}
}

// A workspace-dir .plect/workflows/ layer may only add [[nodes]] (see
// TestLoadWorkflows_WorkspaceDirLayerNodesOnly), so these use BaseDir, the
// trusted layer max_up_children is actually meant for.
func TestLoadWorkflows_MaxUpChildrenRejectsZero(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "capped.toml"), `
[capped]
kind = "workflow"
resource = "issue"
max_up_children = 0
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkflows(t.TempDir())
	if err == nil {
		t.Fatal("expected LoadWorkflows to reject max_up_children = 0")
	}
	if !strings.Contains(err.Error(), "max_up_children") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestLoadWorkflows_MaxUpChildrenRejectsNegative(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "capped.toml"), `
[capped]
kind = "workflow"
resource = "issue"
max_up_children = -1
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkflows(t.TempDir())
	if err == nil {
		t.Fatal("expected LoadWorkflows to reject max_up_children = -1")
	}
	if !strings.Contains(err.Error(), "max_up_children") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestLoadWorkflows_MaxUpChildrenAcceptsPositive(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "capped.toml"), `
[capped]
kind = "workflow"
resource = "issue"
max_up_children = 7
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadWorkflows(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf, ok := got["capped"]
	if !ok {
		t.Fatalf("expected capped in %+v", got)
	}
	if wf.MaxUpChildren == nil || *wf.MaxUpChildren != 7 {
		t.Errorf("MaxUpChildren = %v, want 7", wf.MaxUpChildren)
	}
}

func TestLoadWorkflows_NameAndDescription(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "coding_claude.toml"), `
[coding_claude]
kind = "workflow"
resource = "issue"
name        = "Coding agent (Claude)"
description = "Spawn tmux + Claude Code."

[[coding_claude.nodes]]
id = "tmux"
uses = "tmux"
`)
	// Declaring layer must be trusted: load from a workspace dir one level below.
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	wf := got["coding_claude"]
	if wf.Name != "Coding agent (Claude)" {
		t.Errorf("Name = %q, want %q", wf.Name, "Coding agent (Claude)")
	}
	if wf.Description != "Spawn tmux + Claude Code." {
		t.Errorf("Description = %q, want %q", wf.Description, "Spawn tmux + Claude Code.")
	}
}

func TestLoadWorkflows_NameInDeeperLayerWinsWhenAbsentAtTop(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "g"
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
name        = "From deeper"
description = "deeper desc"

[[shared.nodes]]
uses = "s"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	wf := got["shared"]
	if wf.Name != "From deeper" {
		t.Errorf("Name = %q, want From deeper (deeper layer can fill an unset field)", wf.Name)
	}
	if wf.Description != "deeper desc" {
		t.Errorf("Description = %q, want deeper desc", wf.Description)
	}
}

func TestLoadWorkflows_NameRedeclarationAcrossLayersRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
name = "Global"

[[shared.nodes]]
uses = "g"
`)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
name = "Local"

[[shared.nodes]]
uses = "s"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err == nil {
		t.Fatal("expected error when `name` is redeclared across cascade layers")
	}
}

func TestLoadTaskDefinitions(t *testing.T) {
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "tasks", "tmux.toml"), `
[tmux]
kind = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo '{}'"

[tmux.health.alive]
type = "noop"
`)
	// Task shell must come from a trusted layer — load from a workspace dir one
	// level below the declaring overlay.
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadTaskDefinitions(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	def, ok := got["tmux"]
	if !ok {
		t.Fatalf("expected tmux def, got %+v", got)
	}
	if def.ID != "tmux" {
		t.Errorf("def.ID = %q, want tmux", def.ID)
	}
	if def.EffectiveScope() != TaskScopeRun {
		t.Errorf("scope = %q, want %q", def.EffectiveScope(), TaskScopeRun)
	}
}

func TestLoadTaskDefinitions_DeeperWinsAcrossCascade(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "tasks", "tmux.toml"), `
[tmux]
kind = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo global"

[tmux.health.alive]
type = "noop"
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "tasks", "tmux.toml"), `
[tmux]
kind = "effect"
scope = "session"

[tmux.setup]
type   = "shell"
script = "echo session"

[tmux.health.alive]
type = "noop"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadTaskDefinitions(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	def := got["tmux"]
	if got := def.Setup.Source(); got != "echo session" {
		t.Errorf("Setup = %q, want %q (deeper layer should win)", got, "echo session")
	}
	if def.EffectiveScope() != TaskScopeSession {
		t.Errorf("Scope = %q, want session (deeper layer should win)", def.EffectiveScope())
	}
}

// Coexistence follows from addressability. A hand-authored plugin_dirs entry
// carries no catalog identity, so no address can name one of these two layers
// apart from the other, and choosing between them would fall to declaration
// order.
func TestLoadTaskDefinitions_TwoUnaddressablePluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "config", "tasks", "tmux.toml"), `
[tmux]
kind = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo a"
`)
	writeFile(t, filepath.Join(pluginB, "config", "tasks", "tmux.toml"), `
[tmux]
kind = "effect"
scope = "run"

[tmux.setup]
type   = "shell"
script = "echo b"
`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadTaskDefinitions("")
	if err == nil || !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("expected a collision error naming \"tmux\", got %v", err)
	}
}

func TestLoadTaskDefinitions_HyphenIDRejected(t *testing.T) {
	// An effect id is also a workflow node id when a node omits `id`, and a
	// node id must be a safe dotted path segment — so a hyphen is rejected
	// where the id is declared, not where a workflow references it.
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, ".plect", "tasks", "slack.toml"), `
["slack-thread"]
kind  = "effect"
scope = "session"

["slack-thread".setup]
type   = "shell"
script = "true"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	_, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadTaskDefinitions(filepath.Join(workspaceDirPath, "cwd"))
	if err == nil {
		t.Fatal("expected error when a definition id contains a hyphen")
	}
}

func TestLoadWorkflows_DoneWhenRetiredInAnyLayer(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "g"
`)
	// Even a machine-owned ancestor overlay (trusted layer) may no longer
	// declare workflow-level done_when — it's retired everywhere, not just
	// rejected in the untrusted workspace-dir layer.
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
done_when = "retired"

[[shared.nodes]]
uses = "r"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err == nil {
		t.Fatal("expected error: workflow-level done_when is retired")
	}
	if !strings.Contains(err.Error(), "done_when") {
		t.Errorf("unexpected message: %v", err)
	}
}

// TestLoadWorkflows_TickStaleWhenKeyRejected proves the retired `stale_when`
// / `max_stale_when` TOML keys fail fast with a clear message instead of
// being silently dropped by the decoder (which would leave a workflow's
// heartbeat quietly inactive).
func TestLoadWorkflows_TickStaleWhenKeyRejected(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "stale_when",
			toml: `
[shared]
kind = "workflow"
resource = "issue"
[shared.tick]
stale_when = "15m"
`,
			want: "stale_when",
		},
		{
			name: "max_stale_when",
			toml: `
[shared]
kind = "workflow"
resource = "issue"
[shared.tick]
heartbeat      = "15m"
max_stale_when = "1h"
`,
			want: "max_stale_when",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
			globalDir := filepath.Join(tmpHome, ".config", "plect")
			if err := os.MkdirAll(globalDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
			writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), tt.toml)
			workspaceDirPath := filepath.Join(repoDir, "session")
			if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			_, err = trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
			if err == nil {
				t.Fatalf("expected error: %s is retired", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("unexpected message: %v", err)
			}
		})
	}
}

// TestLoadWorkflows_TickCascadeDeeperWinsWholeTable proves that a
// deeper layer's `[tick]` replaces a shallower layer's wholesale — no
// partial merge of `on`/`heartbeat` across layers, unlike the additive/
// no-redeclare fields (name, done_when, resource, ...).
func TestLoadWorkflows_TickCascadeDeeperWinsWholeTable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[shared.tick]
on        = ["resource.*"]
heartbeat = "15m"
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[shared.tick]
heartbeat = "5m"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	tick := got["shared"].Tick
	if tick == nil {
		t.Fatal("expected a merged [tick] table")
	}
	if len(tick.On) != 0 {
		t.Errorf("on = %v, want empty — the deeper layer replaces the whole table, not just heartbeat", tick.On)
	}
	if tick.Heartbeat.Duration != 5*time.Minute {
		t.Errorf("heartbeat = %v, want 5m from the deeper layer", tick.Heartbeat.Duration)
	}
}

// TestLoadWorkflows_TickGlobalDefaultAfterDeeperDeclarationIsNotAnError covers
// the amendment's rejected alternative: adding a shallower-layer default after
// a deeper layer already declared `[tick]` must not become a load error (a
// global default landing after per-repo tuning already exists must not brick
// every repo that customized it).
func TestLoadWorkflows_TickGlobalDefaultAfterDeeperDeclarationIsNotAnError(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[shared.tick]
on = ["resource.*"]
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[shared.tick]
heartbeat = "30m"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	tick := got["shared"].Tick
	if tick == nil || tick.Heartbeat.Duration != 30*time.Minute || len(tick.On) != 0 {
		t.Fatalf("tick = %+v, want the deeper (ancestor) layer's declaration to win wholesale", tick)
	}
}

func TestLoadWorkflows_TickBackoffResetDefaultsToInboundAndFingerprint(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "w.toml"), `
[w]
kind = "workflow"
resource = "issue"
[w.tick]
heartbeat = "15m"
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadWorkflows(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	tick := got["w"].Tick
	if tick == nil {
		t.Fatal("expected a [tick] table")
	}
	want := []string{"inbound", "fingerprint"}
	if got := tick.BackoffResetOrDefault(); !slices.Equal(got, want) {
		t.Errorf("BackoffResetOrDefault() = %v, want %v", got, want)
	}
}

func TestLoadWorkflows_TickBackoffResetDeclaredReplacesDefault(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "w.toml"), `
[w]
kind = "workflow"
resource = "issue"
[w.tick]
heartbeat     = "15m"
backoff_reset = ["fingerprint", "live_children"]
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadWorkflows(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	tick := got["w"].Tick
	if tick == nil {
		t.Fatal("expected a [tick] table")
	}
	want := []string{"fingerprint", "live_children"}
	if got := tick.BackoffResetOrDefault(); !slices.Equal(got, want) {
		t.Errorf("BackoffResetOrDefault() = %v, want %v", got, want)
	}
}

func TestLoadWorkflows_TickBackoffResetRejectsEmptyList(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "w.toml"), `
[w]
kind = "workflow"
resource = "issue"
[w.tick]
heartbeat     = "15m"
backoff_reset = []
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkflows(t.TempDir())
	if err == nil {
		t.Fatal("expected LoadWorkflows to reject an empty backoff_reset — a tick that can never reset is a misdeclaration")
	}
	if !strings.Contains(err.Error(), "backoff_reset") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestLoadWorkflows_TickBackoffResetRejectsUnknownName(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "w.toml"), `
[w]
kind = "workflow"
resource = "issue"
[w.tick]
heartbeat     = "15m"
backoff_reset = ["nope"]
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkflows(t.TempDir())
	if err == nil {
		t.Fatal("expected LoadWorkflows to reject an unknown backoff_reset name")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestLoadWorkflows_HealthcheckCascadeDeeperWinsWholeTable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[shared.healthcheck]
period = "10m"
stall_threshold = "30m"
renotify_every = 4
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[shared.healthcheck]
period = "2m"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadWorkflows(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	healthcheck := NormalizeHealthcheckConfig(got["shared"].Healthcheck)
	if healthcheck.Period.Duration != 2*time.Minute {
		t.Fatalf("period = %v, want 2m from the deeper layer", healthcheck.Period.Duration)
	}
	if healthcheck.StallThreshold.Duration != 6*time.Minute || healthcheck.RenotifyEvery != 3 {
		t.Fatalf("healthcheck = %+v, want deeper table plus defaults", healthcheck)
	}
}

func TestLoadWorkflows_HealthRepairAttempts(t *testing.T) {
	tests := []struct {
		name     string
		table    string
		want     int
		wantFail bool
	}{
		{"absent", "", 0, false},
		{"disabled", "[w.health]\nauto_repair_attempts = 0", 0, false},
		{"enabled", "[w.health]\nauto_repair_attempts = 3", 3, false},
		{"negative", "[w.health]\nauto_repair_attempts = -1", 0, true},
		{"fractional", "[w.health]\nauto_repair_attempts = 1.5", 0, true},
		{"string", "[w.health]\nauto_repair_attempts = \"3\"", 0, true},
		{"empty", "[w.health]", 0, true},
		{"unknown", "[w.health]\nauto_repair_attempts = 1\nunknown = true", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "workflows", "w.toml"), "[w]\nkind = \"workflow\"\nresource = \"issue\"\n"+tt.table+"\n")
			got, err := (&Config{BaseDir: base}).LoadWorkflows(t.TempDir())
			if tt.wantFail {
				if err == nil {
					t.Fatal("expected invalid workflow health declaration to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["w"].Health.AutoRepairAttempts != tt.want {
				t.Fatalf("auto_repair_attempts = %d, want %d", got["w"].Health.AutoRepairAttempts, tt.want)
			}
		})
	}
}

func TestLoadWorkflows_ResumeInstruction(t *testing.T) {
	for _, tt := range []struct {
		name, value, want string
		fail              bool
	}{
		{"default", "", DefaultResumeInstruction, false},
		{"override", `resume_instruction = "Check before continuing."`, "Check before continuing.", false},
		{"disabled", `resume_instruction = ""`, "", false},
		{"wrong type", `resume_instruction = 1`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "workflows", "w.toml"), "[w]\nkind = \"workflow\"\nresource = \"issue\"\n[w.health]\nauto_repair_attempts = 0\n"+tt.value+"\n")
			got, err := (&Config{BaseDir: base}).LoadWorkflows(t.TempDir())
			if tt.fail {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["w"].Health.ResumeInstruction != tt.want {
				t.Fatalf("resume instruction = %q, want %q", got["w"].Health.ResumeInstruction, tt.want)
			}
		})
	}
}

func TestLoadWorkflows_HealthRepairOverlayCanDisableInheritedPermission(t *testing.T) {
	for _, tt := range []struct {
		name            string
		overlay         string
		want            int
		wantInstruction string
	}{
		{"omitted", "[w]\nkind = \"workflow\"\n", 3, "Global notice"},
		{"replaced", "[w]\nkind = \"workflow\"\n[w.health]\nauto_repair_attempts = 0\n", 0, DefaultResumeInstruction},
		{"overridden", "[w]\nkind = \"workflow\"\n[w.health]\nauto_repair_attempts = 0\nresume_instruction = \"Local notice\"\n", 0, "Local notice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			globalDir := filepath.Join(home, ".config", "plect")
			writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
			writeFile(t, filepath.Join(globalDir, "workflows", "w.toml"), "[w]\nkind = \"workflow\"\nresource = \"issue\"\n[w.health]\nauto_repair_attempts = 3\nresume_instruction = \"Global notice\"\n")
			projectDir := filepath.Join(home, "projects", "project")
			writeFile(t, filepath.Join(projectDir, ".plect", "workflows", "w.toml"), tt.overlay)
			workspace := filepath.Join(projectDir, "session")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			got, err := trustAncestors(cfg, filepath.Join(workspace, "cwd")).LoadWorkflows(filepath.Join(workspace, "cwd"))
			if err != nil {
				t.Fatal(err)
			}
			if got["w"].Health.AutoRepairAttempts != tt.want {
				t.Fatalf("auto_repair_attempts = %d, want %d", got["w"].Health.AutoRepairAttempts, tt.want)
			}
			if got["w"].Health.ResumeInstruction != tt.wantInstruction {
				t.Fatalf("resume_instruction = %q, want %q", got["w"].Health.ResumeInstruction, tt.wantInstruction)
			}
		})
	}
}

func TestLoadTaskDefinitions_AncestorLayerStillTrusted(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "tasks", "teardown.toml"), `
[teardown]
kind = "effect"

[teardown.setup]
type   = "shell"
script = "echo '{}'"

[teardown.health.alive]
type = "noop"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	defs, err := trustAncestors(cfg, filepath.Join(workspaceDirPath, "cwd")).LoadTaskDefinitions(filepath.Join(workspaceDirPath, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := defs["teardown"]; !ok {
		t.Error("repoDir overlay tasks (machine-owned) must keep loading")
	}
}

func TestLoadWorkflows_PluginLayerIsBase(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(tmpHome, "plugin-github")
	writeFile(t, filepath.Join(pluginDir, "config", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
name     = "From plugin"

[[shared.nodes]]
uses = "p"
`)
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("schema_version = 2\nplugin_dirs = [\""+pluginDir+"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
[shared]
kind = "workflow"
[[shared.nodes]]
uses = "g"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	wf := got["shared"]
	if wf.Name != "From plugin" || wf.Resource != "issue" {
		t.Errorf("plugin layer fields lost: %+v", wf)
	}
	if len(wf.Nodes) != 2 || wf.Nodes[0].ID != "p" || wf.Nodes[1].ID != "g" {
		t.Errorf("node order should be plugin → global, got %+v", wf.Nodes)
	}
}

// Coexistence follows from addressability. A hand-authored plugin_dirs entry
// carries no catalog identity, so no address can name one of these two layers
// apart from the other, and choosing between them would fall to declaration
// order.
func TestLoadWorkflows_TwoUnaddressablePluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "config", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "a"
`)
	writeFile(t, filepath.Join(pluginB, "config", "workflows", "shared.toml"), `
[shared]
kind = "workflow"
resource = "issue"
[[shared.nodes]]
uses = "b"
`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("expected a collision error naming \"shared\", got %v", err)
	}
}

func cfgStrp(s string) *string { return &s }

func TestDoneWhenValidate(t *testing.T) {
	tests := []struct {
		name    string
		dw      *DoneWhen
		wantErr bool
	}{
		{name: "nil is valid", dw: nil},
		{name: "single check eq", dw: &DoneWhen{All: []DoneWhenLeaf{{Check: "pr_state", Eq: cfgStrp("merged")}}}},
		{name: "single judge", dw: &DoneWhen{All: []DoneWhenLeaf{{Judge: "approved", ID: "ac"}}}},
		{name: "no leaves", dw: &DoneWhen{}, wantErr: true},
		{name: "check with both kinds", dw: &DoneWhen{All: []DoneWhenLeaf{{Check: "x", Eq: cfgStrp("y"), Judge: "z"}}}, wantErr: true},
		{name: "check with no operator", dw: &DoneWhen{All: []DoneWhenLeaf{{Check: "x"}}}, wantErr: true},
		{name: "check with two operators", dw: &DoneWhen{All: []DoneWhenLeaf{{Check: "x", Eq: cfgStrp("a"), Ne: cfgStrp("b")}}}, wantErr: true},
		{name: "judge with operator", dw: &DoneWhen{All: []DoneWhenLeaf{{Judge: "j", Eq: cfgStrp("a")}}}, wantErr: true},
		{name: "operator without check", dw: &DoneWhen{All: []DoneWhenLeaf{{Eq: cfgStrp("a")}}}, wantErr: true},
		{name: "empty leaf", dw: &DoneWhen{All: []DoneWhenLeaf{{}}}, wantErr: true},
		{name: "single expr", dw: &DoneWhen{All: []DoneWhenLeaf{{Expr: "self.state.verdict_revision == resource.state.revision"}}}},
		{name: "expr with operator", dw: &DoneWhen{All: []DoneWhenLeaf{{Expr: "self.state.x == 1", Eq: cfgStrp("a")}}}, wantErr: true},
		{name: "expr with check", dw: &DoneWhen{All: []DoneWhenLeaf{{Expr: "self.state.x == 1", Check: "x", Eq: cfgStrp("a")}}}, wantErr: true},
		{name: "expr with judge", dw: &DoneWhen{All: []DoneWhenLeaf{{Expr: "self.state.x == 1", Judge: "j"}}}, wantErr: true},
		{name: "expr with judge relation", dw: &DoneWhen{All: []DoneWhenLeaf{{Expr: "self.state.x == 1", Relation: []string{"sibling"}}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dw.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// A definition id is unique within one layer, whichever files the layer
// spreads its declarations across: resolving a same-layer collision by
// traversal order would let a file name decide which of two declarations is
// live. A deeper layer replacing a shallower same-id declaration is the
// cascade rule and stays allowed.
func TestLoadTaskDefinitions_SameLayerDuplicateIDRejected(t *testing.T) {
	const runtime = `
[runtime]
kind = "effect"

[runtime.setup]
type   = "shell"
script = "true"
`
	t.Run("two files in the global layer", func(t *testing.T) {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "tasks", "a.toml"), runtime)
		writeFile(t, filepath.Join(base, "tasks", "b.toml"), runtime)
		_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
		if err == nil {
			t.Fatal("expected a duplicate-id error, got nil")
		}
		if !strings.Contains(err.Error(), "PLECTURE-CFG-ID-DUPLICATE") {
			t.Errorf("error = %v, want it to report the duplicate id", err)
		}
	})
	t.Run("two files in one plugin layer", func(t *testing.T) {
		plugin := t.TempDir()
		writeFile(t, filepath.Join(plugin, "config", "tasks", "a.toml"), runtime)
		writeFile(t, filepath.Join(plugin, "config", "tasks", "b.toml"), runtime)
		_, err := (&Config{PluginDirs: []string{plugin}}).LoadTaskDefinitions("")
		if err == nil {
			t.Fatal("expected a duplicate-id error, got nil")
		}
		if !strings.Contains(err.Error(), "PLECTURE-CFG-ID-DUPLICATE") {
			t.Errorf("error = %v, want it to report the duplicate id", err)
		}
	})
	t.Run("a deeper layer still replaces a shallower declaration", func(t *testing.T) {
		repoDir := t.TempDir()
		writeFile(t, filepath.Join(repoDir, ".plect", "tasks", "runtime.toml"), runtime)
		writeFile(t, filepath.Join(repoDir, "overlay", ".plect", "tasks", "runtime.toml"), `
[runtime]
kind = "effect"

[runtime.setup]
type   = "shell"
script = "echo deeper"

[runtime.health.alive]
type = "noop"
`)
		workspaceDirPath := filepath.Join(repoDir, "overlay", "session")
		if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
			t.Fatal(err)
		}
		defs, err := trustAncestors(&Config{}, filepath.Join(workspaceDirPath, "cwd")).LoadTaskDefinitions(filepath.Join(workspaceDirPath, "cwd"))
		if err != nil {
			t.Fatalf("LoadTaskDefinitions: %v", err)
		}
		if got := defs["runtime"].Setup.Source(); got != "echo deeper" {
			t.Errorf("Setup = %q, want the deeper layer's declaration", got)
		}
	})
}

// Identity is the definition table's name, so one document declares as many
// workflows as its author wants — the shape a filename-keyed loader could not
// express.
func TestLoadWorkflows_OneDocumentDeclaresSeveral(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "agents.toml"), `
[coding]
kind = "workflow"
resource = "issue"
name = "Coding agent"

[[coding.nodes]]
uses = "tmux"

[reviewing]
kind = "workflow"
resource = "issue"
name = "Reviewing agent"

[[reviewing.nodes]]
uses = "tmux"
`)
	got, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d workflows, want 2: %+v", len(got), got)
	}
	for id, name := range map[string]string{"coding": "Coding agent", "reviewing": "Reviewing agent"} {
		wf, ok := got[id]
		if !ok {
			t.Fatalf("expected %s in %+v", id, got)
		}
		if wf.Name != name {
			t.Errorf("%s name = %q, want %q", id, wf.Name, name)
		}
		if wf.SourcePath == "" || wf.Definition == nil || wf.Definition.ID != id {
			t.Errorf("%s carries no parsed declaration: %+v", id, wf)
		}
	}
}

// A display value reads persisted outputs and the session's inputs only, so
// its freshness costs no network: a projection of a live root is refused at
// load rather than resolving to a blank in a listing.
func TestLoadWorkflows_DisplayRejectsAForeignRoot(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"

[coding.display]
title = { from = "nodes.tmux.outputs.session_name" }

[[coding.nodes]]
uses = "tmux"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-FROM-ROOT") {
		t.Fatalf("LoadWorkflows = %v, want a foreign-root diagnostic", err)
	}
}

// A workflow declares the one entry resource it accepts; a workflow with none
// can never be selected for a session, so it does not load.
func TestLoadWorkflows_ResourceIsRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"

[[coding.nodes]]
uses = "tmux"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "`resource` is required") {
		t.Fatalf("LoadWorkflows error = %v, want the missing entry resource named", err)
	}
}

// workdir is read as one projection from a node output.
func TestLoadWorkflows_WorkdirIsDecodedAsANodeOutputProjection(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind     = "workflow"
resource = "issue"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[coding.nodes]]
id   = "checkout"
uses = "checkout_effect"
`)
	got, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	workdir := got["coding"].Workdir
	if workdir == nil || workdir.Node != "checkout" || workdir.Key != "workspace_dir" {
		t.Errorf("Workdir = %+v, want checkout.workspace_dir", workdir)
	}
}

// The retired provider field is refused with a pointer, never read as unknown.
func TestLoadWorkflows_RetiredWorkspaceProviderFieldPointsAtTheMigration(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind               = "workflow"
resource           = "issue"
workspace_provider = "worktree"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "workspace_provider") || !strings.Contains(err.Error(), "docs/migrations/") {
		t.Fatalf("LoadWorkflows error = %v, want the retired field named with a pointer to the migration guide", err)
	}
}

// A contract declared twice is a load-time question: leaving it to the
// compile at `plect create` time would let the ambiguity ship.
func TestLoadWorkflows_InlineAndFileInputsSchemaMutuallyExclusive(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind               = "workflow"
resource               = "issue"
inputs_schema_file = "coding.schema.json"

[coding.inputs_schema]
type = "object"

[[coding.nodes]]
uses = "tmux"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("LoadWorkflows = %v, want a mutually-exclusive error", err)
	}
}

// A channel binding that is not an array of tables used to load as no
// channels at all, which leaves the session unreachable with nothing said.
func TestLoadWorkflows_NonArrayEventChannelRejected(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"

[coding.event]
channel = "runtime"

[[coding.nodes]]
uses = "tmux"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-FIELD-TYPE") {
		t.Fatalf("LoadWorkflows = %v, want a field-type diagnostic", err)
	}
}

// A binding with no name cannot be addressed and one with no target delivers
// nowhere, so the load refuses both rather than leaving them to whichever
// consumer notices the gap first.
func TestLoadWorkflows_EventChannelRequiresNameAndUses(t *testing.T) {
	for _, entry := range []string{"uses = \"delivery\"\n", "name = \"runtime\"\n"} {
		baseDir := t.TempDir()
		writeFile(t, filepath.Join(baseDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"

[[coding.nodes]]
uses = "tmux"

[[coding.event.channel]]
`+entry)
		_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
		if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-FIELD-REQUIRED") {
			t.Fatalf("LoadWorkflows with %q = %v, want a required-field diagnostic", entry, err)
		}
	}
}

// Id uniqueness across kinds is a rule within a layer, so every loader has to
// see a same-layer collision: filtering by kind before comparing ids would let
// each hold a winner with neither knowing about the other.
func TestLoadDeclarations_SameLayerCrossKindCollisionRejected(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "tasks", "shared.toml"),
		"[shared]\nkind = \"effect\"\n\n[shared.setup]\ntype = \"shell\"\nscript = \"true\"\n")
	writeFile(t, filepath.Join(base, "channels", "shared.toml"),
		"[shared]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n")
	cfg := &Config{BaseDir: base}

	for _, load := range []struct {
		name string
		run  func() error
	}{
		{"LoadChannels", func() error { _, err := cfg.LoadChannels(); return err }},
		{"LoadTaskDefinitions", func() error { _, err := cfg.LoadTaskDefinitions(""); return err }},
		{"LoadTaskDocuments", func() error { _, err := cfg.LoadTaskDocuments(""); return err }},
		{"LoadWorkflows", func() error { _, err := cfg.LoadWorkflows(""); return err }},
		{"LoadResourceDefs", func() error { _, err := cfg.LoadResourceDefs(); return err }},
	} {
		t.Run(load.name, func(t *testing.T) {
			err := load.run()
			if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-ID-DUPLICATE") {
				t.Fatalf("%s = %v, want a duplicate-id diagnostic", load.name, err)
			}
		})
	}
}

// Across layers the same pair coexists: each layer has its own namespace, so
// neither declaration shadows the other and each loader sees its own kind.
func TestLoadDeclarations_CrossLayerCrossKindCoexists(t *testing.T) {
	pluginDir := t.TempDir()
	base := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "tasks", "shared.toml"),
		"[shared]\nkind = \"effect\"\n\n[shared.setup]\ntype = \"shell\"\nscript = \"true\"\n\n[shared.health.alive]\ntype = \"noop\"\n")
	writeFile(t, filepath.Join(base, "channels", "shared.toml"),
		"[shared]\nkind = \"channel\"\ntype = \"exec\"\ncommand = \"true\"\n")
	cfg := &Config{BaseDir: base, PluginDirs: []string{pluginDir}}

	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels: %v", err)
	}
	if _, ok := channels["shared"]; !ok {
		t.Errorf("the user-owned channel is missing: %v", channels)
	}
	effects, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if _, ok := effects["shared"]; !ok {
		t.Errorf("the plugin effect is missing: %v", effects)
	}
}

// A filename carries no meaning inside a definition root, so a nested file
// whose basename happens to match a reserved root file is still a definition.
// Reserving by basename at any depth would drop it silently.
func TestLoadTaskDefinitions_NestedReservedBasenameStillLoads(t *testing.T) {
	for _, name := range []string{"config.toml", "catalogs.toml", "plect.lock.toml"} {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "effects", name),
			"[nested]\nkind = \"effect\"\n\n[nested.setup]\ntype = \"shell\"\nscript = \"true\"\n\n[nested.health.alive]\ntype = \"noop\"\n")
		effects, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
		if err != nil {
			t.Fatalf("%s: LoadTaskDefinitions: %v", name, err)
		}
		if _, ok := effects["nested"]; !ok {
			t.Errorf("%s: nested declaration was skipped for its basename", name)
		}
	}
}

// A reserved name is still reserved at the root it belongs to.
func TestLoadTaskDefinitions_RootReservedFileStillSkipped(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "config.toml"), "workspace_dirs_root = \"/tmp/x\"\n")
	if _, err := (&Config{BaseDir: base}).LoadTaskDefinitions(""); err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
}

func TestLoadWorkflows_ReadsPublicOutputsAndTheirSchema(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "review.toml"), `
[review]
kind = "workflow"
resource = "issue"

[review.outputs]
endpoint = { from = "nodes.agent.outputs.endpoint" }
label    = "fixed"

[review.outputs_schema]
type     = "object"
required = ["endpoint"]

[[review.nodes]]
uses = "agent"
`)
	got, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	wf := got["review"]
	if len(wf.Outputs) != 2 {
		t.Fatalf("Outputs = %+v, want two bindings", wf.Outputs)
	}
	if endpoint := wf.Outputs["endpoint"]; endpoint == nil || endpoint.Form != lang.FormFrom || endpoint.From != "nodes.agent.outputs.endpoint" {
		t.Fatalf("endpoint binding = %+v", endpoint)
	}
	if wf.OutputsSchema["type"] != "object" {
		t.Fatalf("OutputsSchema = %+v", wf.OutputsSchema)
	}
}

func TestLoadWorkflows_PublicOutputCycleIsALoadError(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "review.toml"), `
[review]
kind = "workflow"
resource = "issue"

[review.outputs]
endpoint = { from = "nodes.agent.outputs.endpoint" }

[[review.nodes]]
uses = "agent"

[review.nodes.inputs]
peer = { from = "workflow.outputs.endpoint" }
`)
	_, err := (&Config{BaseDir: baseDir}).LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-WORKFLOW-CYCLE") {
		t.Fatalf("LoadWorkflows = %v, want a cycle diagnostic", err)
	}
}

func TestWorkflowRevisionCarriesPublicOutputs(t *testing.T) {
	revision := WorkflowRevision{Workflow: WorkflowFile{
		ID: "review", Address: "review",
		Outputs:       map[string]*lang.Value{"endpoint": {Form: lang.FormFrom, From: "nodes.agent.outputs.endpoint"}},
		OutputsSchema: map[string]any{"type": "object"},
	}}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkflowRevision(body, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Workflow.Outputs["endpoint"]; got == nil || got.From != "nodes.agent.outputs.endpoint" {
		t.Fatalf("decoded outputs = %+v", decoded.Workflow.Outputs)
	}
	if decoded.Workflow.OutputsSchema["type"] != "object" {
		t.Fatalf("decoded outputs schema = %+v", decoded.Workflow.OutputsSchema)
	}
}

// The invocation directory can be a generated checkout, so its own `.plect/`
// is a layer only when it is listed as a trusted root, however an ancestor's
// is treated.
func TestLoadDefinitions_InvocationDirsOwnLayerNeedsItsOwnTrust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\nresource = \"issue\"\n[[shared.nodes]]\nuses = \"base\"\n")
	above := filepath.Join(home, "projects")
	invocation := filepath.Join(above, "checkout")
	writeFile(t, filepath.Join(above, ".plect", "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\n[[shared.nodes]]\nuses = \"overlay\"\n")
	writeFile(t, filepath.Join(invocation, ".plect", "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\n[[shared.nodes]]\nuses = \"clone\"\n")
	writeFile(t, filepath.Join(invocation, ".plect", "tasks", "shell.toml"), "[shell]\nkind = \"effect\"\n[shell.setup]\ntype = \"shell\"\nscript = \"true\"\n[shell.health.alive]\ntype = \"noop\"\n")

	for _, tc := range []struct {
		name    string
		trusted []string
		want    string
		effect  bool
	}{
		{"only the ancestor is trusted", []string{above}, "base,overlay", false},
		{"the invocation directory is trusted too", []string{above, invocation}, "base,overlay,clone", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.TrustedProjectRoots = tc.trusted
			workflows, err := cfg.LoadWorkflows(invocation)
			if err != nil {
				t.Fatalf("LoadWorkflows: %v", err)
			}
			var uses []string
			for _, node := range workflows["shared"].Nodes {
				uses = append(uses, node.Uses)
			}
			if strings.Join(uses, ",") != tc.want {
				t.Errorf("nodes = %v, want %s", uses, tc.want)
			}
			effects, err := cfg.LoadTaskDefinitions(invocation)
			if err != nil {
				t.Fatalf("LoadTaskDefinitions: %v", err)
			}
			if _, ok := effects["shell"]; ok != tc.effect {
				t.Errorf("effect loaded = %v, want %v", ok, tc.effect)
			}
		})
	}
}

// A chain or a population has no caller directory, so it sees the plugin and
// global layers alone.
func TestLoadDefinitions_NoInvocationDirReadsNoOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\nresource = \"issue\"\n[[shared.nodes]]\nuses = \"base\"\n")
	writeFile(t, filepath.Join(home, ".plect", "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\n[[shared.nodes]]\nuses = \"overlay\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	if got := workflows["shared"].Nodes; len(got) != 1 || got[0].Uses != "base" {
		t.Errorf("nodes = %+v, want only the global layer's", got)
	}
}

func TestLoadDefinitions_TrustedProjectRootMatchesThroughASymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "projects", "real")
	writeFile(t, filepath.Join(real, ".plect", "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\n[[shared.nodes]]\nuses = \"overlay\"\n")
	if err := os.MkdirAll(filepath.Join(real, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "workflows", "shared.toml"), "[shared]\nkind = \"workflow\"\nresource = \"issue\"\n[[shared.nodes]]\nuses = \"base\"\n")

	for name, tc := range map[string]struct {
		trusted    string
		invocation string
	}{
		"trusted by its alias, invoked by its real path": {alias, filepath.Join(real, "src")},
		"trusted by its real path, invoked by its alias": {real, filepath.Join(alias, "src")},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{BaseDir: base, TrustedProjectRoots: []string{tc.trusted}}
			workflows, err := cfg.LoadWorkflows(tc.invocation)
			if err != nil {
				t.Fatalf("LoadWorkflows: %v", err)
			}
			if got := len(workflows["shared"].Nodes); got != 2 {
				t.Errorf("nodes = %d, want the trusted overlay's node appended to the global one", got)
			}
		})
	}
}

func TestLoad_RelativeTrustedProjectRootIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".config", "plect", "config.toml"), "schema_version = 2\ntrusted_project_roots = [\"projects/widgets\"]\n")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "trusted_project_roots") {
		t.Fatalf("Load = %v, want an error naming trusted_project_roots", err)
	}
}
