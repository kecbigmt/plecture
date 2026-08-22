package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

const observerObserveHook = `
[github.observe]
type    = "exec"
command = "true"
args    = ["observe", { from = "resource.id" }]
`

func TestLoadResourceDefs_GlobalLayer(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "github.toml"), `
[github]
kind  = "resource_observer"
match = '^https://github\.com/'
`+observerObserveHook)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := got["github"]
	if !ok {
		t.Fatalf("expected github resource observer, got %+v", got)
	}
	if def.Match == "" || def.Observe == nil || def.Observe.Type != lang.ActionExec {
		t.Errorf("fields lost: %+v", def)
	}
	if def.FromPlugin {
		t.Error("a global-layer definition is not plugin-owned")
	}
}

// The id is the declaration's table name: under the ratified language a
// file's name and directory carry no identity, so one document may declare
// several observers and none of them is named after the file.
func TestLoadResourceDefs_IDComesFromTheTableNotTheFilename(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "anything.toml"), `
[issue]
kind  = "resource_observer"
match = '^https://github\.com/[^/]+/[^/]+/issues/'

[issue.observe]
type    = "exec"
command = "true"

[pull]
kind  = "resource_observer"
match = '^https://github\.com/[^/]+/[^/]+/pull/'

[pull.observe]
type    = "exec"
command = "true"
`)
	got, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"issue", "pull"} {
		if _, ok := got[id]; !ok {
			t.Errorf("expected observer %q, got %v", id, got)
		}
	}
	if _, ok := got["anything"]; ok {
		t.Error("the filename must not become an id")
	}
}

func TestLoadResourceDefs_GlobalOverridesPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "github.toml"), `
[github]
kind  = "resource_observer"
match = '^x'

[github.observe]
type    = "exec"
command = "plugin"
`)
	writeFile(t, filepath.Join(baseDir, "resources", "github.toml"), `
[github]
kind  = "resource_observer"
match = '^x'

[github.observe]
type    = "exec"
command = "global"
`)
	cfg := &Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	if got["github"].Observe.Command != "global" {
		t.Errorf("Command = %q, want global to win (deeper layer)", got["github"].Observe.Command)
	}
}

func TestLoadResourceDefs_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	for _, dir := range []string{pluginA, pluginB} {
		writeFile(t, filepath.Join(dir, "config", "resources", "github.toml"), `
[github]
kind  = "resource_observer"
match = '^x'

[github.observe]
type    = "exec"
command = "true"
`)
	}
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"github\", got %v", err)
	}
}

func TestLoadResourceDefs_RejectedDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "match required",
			body: "[broken]\nkind = \"resource_observer\"\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\n",
			want: "match",
		},
		{
			name: "observe required",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\n",
			want: "observe",
		},
		{
			name: "match does not compile",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '('\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\n",
			want: "match",
		},
		{
			name: "kind missing",
			body: "[broken]\nmatch = '^x'\n",
			want: "kind",
		},
		{
			name: "another kind under resources",
			body: "[broken]\nkind = \"effect\"\nscope = \"run\"\n",
			want: "resource_observer",
		},
		{
			name: "a field outside the observer surface",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\nexecution = \"environment\"\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\n",
			want: "execution",
		},
		{
			name: "a root the observe surface does not offer",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\nargs = [{ from = \"session.name\" }]\n",
			want: "session.name",
		},
		{
			name: "a root the finalize surface does not offer",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\n\n[broken.finalize]\ntype = \"exec\"\ncommand = \"true\"\nargs = [{ from = \"workspace.dir\" }]\n",
			want: "workspace.dir",
		},
		{
			name: "an interpolated shell script",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\n\n[broken.observe]\ntype = \"shell\"\nscript = \"echo {{.ResourceID}}\\n\"\n",
			want: "bind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			writeFile(t, filepath.Join(baseDir, "resources", "broken.toml"), tt.body)
			_, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
			if err == nil {
				t.Fatalf("expected a load error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not mention %q: %v", tt.want, err)
			}
		})
	}
}

func TestLoadResourceDefs_NotInWorkspaceDirCascade(t *testing.T) {
	// Resource observers are trusted-base-layer only, mirroring workspace
	// providers — a resources/ dir inside a workspace-dir overlay chain must
	// never be picked up (ADR "goal-as-task" D6: observation runs arbitrary
	// executables).
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "resources", "evil.toml"), `
[evil]
kind  = "resource_observer"
match = '.*'

[evil.observe]
type    = "exec"
command = "curl"
args    = ["evil.example"]
`)
	cfg := &Config{}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok {
		t.Fatal("workspace-dir-layer resource observer must never load")
	}
}

// Shipped plugin config cannot know the alias a user registered its catalog
// under, so a plugin-owned observer naming another plugin's executable is a
// reference error rather than a lookup that happens to fail.
func TestLoadResourceDefs_PluginOwnedBinRefStaysInsideItsPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "github.toml"), `
[github]
kind  = "resource_observer"
match = '^gh:'

[github.observe]
type = "exec"
bin  = "local/runtime/watcher"
`)
	_, err := (&Config{PluginDirs: []string{pluginDir}}).LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "bare name") {
		t.Fatalf("expected a cross-plugin reference error, got %v", err)
	}
}
