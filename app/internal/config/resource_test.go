package config

import (
	"errors"
	"fmt"
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

// Coexistence follows from addressability. A hand-authored plugin_dirs entry
// carries no catalog identity, so no address can name one of these two layers
// apart from the other, and choosing between them would fall to declaration
// order.
func TestLoadResourceDefs_TwoUnaddressablePluginLayersSameIDFailsLoud(t *testing.T) {
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
		t.Fatalf("expected a collision error naming \"github\", got %v", err)
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
		{
			name: "malformed resource inputs schema",
			body: "[broken]\nkind = \"resource_observer\"\nmatch = '^x'\n\n[broken.observe]\ntype = \"exec\"\ncommand = \"true\"\n\n[broken.inputs_schema]\ntype = \"bogus\"\n",
			want: "inputs_schema",
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

// An id is declared by the document, not by the filename, so two files in
// one layer can claim the same id. That is the language's per-layer
// duplicate, and file order must not silently pick a winner.
func TestLoadResourceDefs_SameLayerDuplicateIDAcrossFiles(t *testing.T) {
	const body = `
[same]
kind  = "resource_observer"
match = '^x'

[same.observe]
type    = "exec"
command = "%s"
`
	t.Run("global layer", func(t *testing.T) {
		baseDir := t.TempDir()
		writeFile(t, filepath.Join(baseDir, "resources", "a.toml"), fmt.Sprintf(body, "first"))
		writeFile(t, filepath.Join(baseDir, "resources", "b.toml"), fmt.Sprintf(body, "second"))
		_, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
		var diag *lang.Diagnostic
		if !errors.As(err, &diag) || diag.Code != lang.CodeIDDuplicate {
			t.Fatalf("expected %s, got %v", lang.CodeIDDuplicate, err)
		}
	})

	t.Run("one plugin layer", func(t *testing.T) {
		pluginDir := t.TempDir()
		writeFile(t, filepath.Join(pluginDir, "config", "resources", "a.toml"), fmt.Sprintf(body, "first"))
		writeFile(t, filepath.Join(pluginDir, "config", "resources", "b.toml"), fmt.Sprintf(body, "second"))
		_, err := (&Config{PluginDirs: []string{pluginDir}}).LoadResourceDefs()
		var diag *lang.Diagnostic
		if !errors.As(err, &diag) || diag.Code != lang.CodeIDDuplicate {
			t.Fatalf("expected %s, got %v", lang.CodeIDDuplicate, err)
		}
	})
}

// The global layer still replaces a plugin layer's same-id definition — that
// is the cascade, not a duplicate.
func TestLoadResourceDefs_GlobalReplacingAPluginIDIsNotADuplicate(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "a.toml"), `
[same]
kind  = "resource_observer"
match = '^x'

[same.observe]
type    = "exec"
command = "plugin"
`)
	writeFile(t, filepath.Join(baseDir, "resources", "b.toml"), `
[same]
kind  = "resource_observer"
match = '^x'

[same.observe]
type    = "exec"
command = "global"
`)
	got, err := (&Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}).LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	if got["same"].Observe.Command != "global" {
		t.Errorf("Command = %q, want the global layer to win", got["same"].Observe.Command)
	}
}

const forgeIssueResource = `
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
name  = { expr = "match.project + '-issue-' + match.number" }

[issue.observe]
type    = "exec"
command = "true"
`

func TestLoadResourceDefs_NameDerivesTheInstanceNameFromMatchCaptures(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "issue.toml"), forgeIssueResource)
	got, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := got["issue"]
	if !ok {
		t.Fatalf("resource not loaded: %v", got)
	}
	name, matched, err := def.InstanceName("https://forge.example/acme/issues/42")
	if err != nil || !matched || name != "acme-issue-42" {
		t.Errorf("InstanceName = (%q, %v, %v), want (acme-issue-42, true, nil)", name, matched, err)
	}
	if _, matched, err := def.InstanceName("https://forge.example/acme/pulls/42"); err != nil || matched {
		t.Errorf("an id the match does not recognize = (matched %v, err %v), want unmatched and no error", matched, err)
	}
}

func TestLoadResourceDefs_NameIsRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "issue.toml"), `
[issue]
kind  = "resource"
match = '^https://forge\.example/'

[issue.observe]
type    = "exec"
command = "true"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "`name` is required") {
		t.Fatalf("error = %v, want a load error saying `name` is required", err)
	}
}

func TestLoadResourceDefs_NameMayNotOutrunItsCaptures(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "issue.toml"), `
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<number>\d+)$'
name  = { from = "match.project" }

[issue.observe]
type    = "exec"
command = "true"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "match.project") {
		t.Fatalf("error = %v, want a load error naming match.project", err)
	}
}

func TestLoadResourceDefs_RetiredKindPointsAtTheMigrationGuide(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "issue.toml"), `
[issue]
kind  = "resource_observer"
match = '^https://forge\.example/'

[issue.observe]
type    = "exec"
command = "true"
`)
	_, err := (&Config{BaseDir: baseDir}).LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "docs/migrations/resource-kind-migration.md") {
		t.Fatalf("error = %v, want a load error pointing at the migration guide", err)
	}
}

// A task document's resource must be a resource declaration: pointing it at
// another kind is rejected when the config loads, not when an instance binds.
func TestLoadTaskDocuments_ResourceNamesAResourceDeclaration(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue.toml"), forgeIssueResource)
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind         = "task"
resource     = "issue"
instructions = [{ text = "Resolve {{ resource.id }}." }]
`)
	docs, err := (&Config{BaseDir: base}).LoadTaskDocuments("")
	if err != nil {
		t.Fatal(err)
	}
	if got := docs["work"].Resource; got != "issue" {
		t.Errorf("Resource = %q, want issue", got)
	}

	writeFile(t, filepath.Join(base, "workflows", "other.toml"), "[issue_flow]\nkind = \"workflow\"\n")
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind         = "task"
resource     = "issue_flow"
instructions = [{ text = "Resolve {{ resource.id }}." }]
`)
	if _, err := (&Config{BaseDir: base}).LoadTaskDocuments(""); err == nil || !strings.Contains(err.Error(), "KIND-MISMATCH") {
		t.Fatalf("error = %v, want PLECTURE-CFG-KIND-MISMATCH", err)
	}
}

func TestWorkflowRevisionBodyRecordsResourceDeclarationsInTheNewForm(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue.toml"), forgeIssueResource)
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `
[work]
kind         = "task"
resource     = "issue"
instructions = [{ text = "Resolve {{ resource.id }}." }]
`)
	docs, err := (&Config{BaseDir: base}).LoadTaskDocuments("")
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := WorkflowRevision{Workflow: WorkflowFile{ID: "work"}, Tasks: docs}.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "resource_observer") || strings.Contains(string(body), "ResourceObserver") {
		t.Errorf("revision body still records the retired field: %s", body)
	}
	if !strings.Contains(string(body), `"Resource":"issue"`) {
		t.Errorf("revision body does not record the task's resource: %s", body)
	}
}

// A resource's name fails with the resource's id, so the declaration an
// author must fix is named, whichever way the derivation broke.
func TestResourceDefInstanceNameFailurePathsNameTheResource(t *testing.T) {
	tests := []struct {
		name  string
		match string
		value map[string]any
		id    string
	}{
		{name: "a match that does not compile", match: `^(`, value: map[string]any{"from": "match.n"}, id: "x"},
		{name: "a name reading an undeclared capture", match: `^x/(?P<n>\d+)$`, value: map[string]any{"from": "match.other"}, id: "x/1"},
		{name: "a name that evaluates to the empty string", match: `^x/(?P<n>\d*)$`, value: map[string]any{"from": "match.n"}, id: "x/"},
		{name: "a name that evaluates to whitespace only", match: `^x/(?P<n>\s+)$`, value: map[string]any{"from": "match.n"}, id: "x/  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, err := lang.ParseValue(tc.value, lang.ClassData, lang.Position{File: "r.toml", Path: "res.name"})
			if err != nil {
				t.Fatal(err)
			}
			res := ResourceDef{ID: "res", Match: tc.match, Name: name}
			got, matched, err := res.InstanceName(tc.id)
			if err == nil || !strings.Contains(err.Error(), `"res"`) {
				t.Fatalf("error = %v, want one naming resource \"res\"", err)
			}
			if got != "" || matched {
				t.Errorf("a failed resolution returned (%q, %v), want no name", got, matched)
			}
		})
	}
}
