package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// writePluginWorkflow stands up one catalog plugin that declares a whole
// workflow — its effect and workspace provider included, so every reference
// inside it is relative and resolves in the plugin's own namespace.
func writePluginWorkflow(t *testing.T, wfID, marker string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, "config", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("tasks", "noop.toml"), `
[noop]
kind  = "effect"
scope = "session"

[noop.setup]
type   = "shell"
script = "echo '{}'"
`)
	write(filepath.Join("workspaces", "space.toml"), `
[space]
kind  = "workspace_provider"
match = '^https://github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/(issues|pull)/(?P<number>\d+)'
name  = { expr = "match.owner + '/' + match.repo + '-' + match.number" }

[space.setup]
type   = "shell"
script = "mkdir -p `+marker+` && printf '{\"workspace_dir\":\"`+marker+`\"}'"
`)
	write(filepath.Join("workflows", wfID+".toml"), `
[`+wfID+`]
kind               = "workflow"
workspace_provider = "space"

[[`+wfID+`.nodes]]
uses = "noop"
`)
	return dir
}

// A session freezes the address its workflow answers to. Freezing the bare id
// would leave the next command looking up a name no map holds — which the
// idempotent second Create exercises directly, since it compares the frozen
// value against the workflow it just resolved.
func TestCreate_PluginOwnedWorkflowFreezesItsAddress(t *testing.T) {
	store := testStore(t)
	dir := writePluginWorkflow(t, "shared", filepath.Join(t.TempDir(), "wd"))
	cfg := &config.Config{
		BaseDir:    t.TempDir(),
		PluginDirs: []string{dir},
		Plugins:    []plugins.Mounted{{ID: "official/acme", Dir: dir}},
	}

	url := "https://github.com/org/repo/issues/42"
	result, err := Create(cfg, store, CreateParams{URL: url, Workflow: "official.acme.shared"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := store.Get(result.SessionName)
	if s == nil {
		t.Fatalf("no session recorded for %q", result.SessionName)
	}
	if s.Workflow != "official.acme.shared" {
		t.Errorf("frozen workflow = %q, want the address it was selected by", s.Workflow)
	}
	// The plan has to reload against the frozen value.
	if _, err := Create(cfg, store, CreateParams{URL: url, Workflow: "official.acme.shared"}); err != nil {
		t.Errorf("second Create: %v", err)
	}
}

// The acceptance case: two plugins declaring one workflow id both run. Their
// session names still need distinct tags, because a workflow id is the default
// session tag and that is naming rather than addressing.
func TestCreate_TwoPluginsSharingAWorkflowIDRunSideBySide(t *testing.T) {
	store := testStore(t)
	dirA := writePluginWorkflow(t, "shared", filepath.Join(t.TempDir(), "a"))
	dirB := writePluginWorkflow(t, "shared", filepath.Join(t.TempDir(), "b"))
	cfg := &config.Config{
		BaseDir:    t.TempDir(),
		PluginDirs: []string{dirA, dirB},
		Plugins: []plugins.Mounted{
			{ID: "official/a", Dir: dirA},
			{ID: "official/b", Dir: dirB},
		},
	}

	url := "https://github.com/org/repo/issues/42"
	for _, tc := range []struct{ address, tag string }{
		{"official.a.shared", "a"},
		{"official.b.shared", "b"},
	} {
		result, err := Create(cfg, store, CreateParams{URL: url, Workflow: tc.address, Tag: tc.tag})
		if err != nil {
			t.Fatalf("Create(%s): %v", tc.address, err)
		}
		s := store.Get(result.SessionName)
		if s == nil || s.Workflow != tc.address {
			t.Fatalf("session %q froze %q, want %q", result.SessionName, s.Workflow, tc.address)
		}
	}
}
