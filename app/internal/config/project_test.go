package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectMarker = "schema_version = 2\n"

func markProject(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ".plect", "project.toml"), projectMarker)
}

func workflowUsing(id, uses string) string {
	return "[" + id + "]\nkind = \"workflow\"\nresource = \"issue\"\n[[" + id + ".nodes]]\nuses = \"" + uses + "\"\n"
}

func nodeUses(t *testing.T, workflows map[string]WorkflowFile, id string) string {
	t.Helper()
	wf, ok := workflows[id]
	if !ok {
		t.Fatalf("workflow %q not loaded", id)
	}
	var uses []string
	for _, node := range wf.Nodes {
		uses = append(uses, node.Uses)
	}
	return strings.Join(uses, ",")
}

func canonicalDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestFindProjectRoot(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "inner")
	markProject(t, outer)
	markProject(t, inner)
	unmarked := filepath.Join(inner, "unmarked")
	writeFile(t, filepath.Join(unmarked, ".plect", "workflows", "w.toml"), workflowUsing("w", "x"))
	invocation := filepath.Join(unmarked, "src")
	if err := os.MkdirAll(invocation, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(inner, link); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(base, "bare")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{"the nearest marked ancestor is the root, past an unmarked .plect/", invocation, inner},
		{"the invocation directory itself can be the root", outer, outer},
		{"a symlinked invocation selects the root's canonical path", link, inner},
		{"no marked ancestor selects no root", bare, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FindProjectRoot(tc.dir)
			if err != nil {
				t.Fatalf("FindProjectRoot: %v", err)
			}
			want := tc.want
			if want != "" {
				want = canonicalDir(t, want)
			}
			if got != want {
				t.Errorf("root = %q, want %q", got, want)
			}
		})
	}
}

func TestSelectProjectRoot_UntrustedRootNamesTheRootToTrust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "widgets")
	markProject(t, root)
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{BaseDir: t.TempDir()}

	_, err := cfg.SelectProjectRoot(sub)
	var untrusted *UntrustedProjectError
	if !errors.As(err, &untrusted) {
		t.Fatalf("SelectProjectRoot = %v, want an UntrustedProjectError", err)
	}
	if untrusted.Root != canonicalDir(t, root) {
		t.Errorf("Root = %q, want %q", untrusted.Root, canonicalDir(t, root))
	}
	if msg := err.Error(); !strings.Contains(msg, canonicalDir(t, root)) || !strings.Contains(msg, "trusted_project_roots") {
		t.Errorf("message %q must name the canonical root and trusted_project_roots", msg)
	}
}

func TestSelectProjectRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "widgets")
	markProject(t, root)
	bare := filepath.Join(base, "bare")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{"a trusted root is selected", root, canonicalDir(t, root)},
		{"no marked ancestor selects the global-only context", bare, ""},
		{"no invocation directory selects the global-only context", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cfg.SelectProjectRoot(tc.dir)
			if err != nil {
				t.Fatalf("SelectProjectRoot: %v", err)
			}
			if got != tc.want {
				t.Errorf("root = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadDefinitions_ProjectLayer(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "workflows", "shared.toml"), workflowUsing("shared", "base"))
	writeFile(t, filepath.Join(global, "workflows", "global_only.toml"), workflowUsing("global_only", "base"))
	projects := t.TempDir()
	outer := filepath.Join(projects, "outer")
	markProject(t, outer)
	writeFile(t, filepath.Join(outer, ".plect", "workflows", "outer.toml"), workflowUsing("outer", "outer"))
	root := filepath.Join(outer, "widgets")
	markProject(t, root)
	writeFile(t, filepath.Join(root, ".plect", "workflows", "shared.toml"), workflowUsing("shared", "project"))
	writeFile(t, filepath.Join(root, ".plect", "tasks", "build.toml"), "[build]\nkind = \"effect\"\n[build.setup]\ntype = \"shell\"\nscript = \"true\"\n[build.health.alive]\ntype = \"noop\"\n")
	cfg := &Config{BaseDir: global, TrustedProjectRoots: []string{outer, root}}

	selected, err := cfg.SelectProjectRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows(selected)
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	if got := nodeUses(t, workflows, "shared"); got != "project" {
		t.Errorf("shared nodes = %q, want the project declaration to replace the global one whole", got)
	}
	if got := nodeUses(t, workflows, "global_only"); got != "base" {
		t.Errorf("global_only nodes = %q, want the global declaration", got)
	}
	if _, ok := workflows["outer"]; ok {
		t.Error("a trusted marked root above the selected one contributed a workflow")
	}
	effects, err := cfg.LoadTaskDefinitions(selected)
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if _, ok := effects["build"]; !ok {
		t.Error("the project layer's effect was not loaded")
	}

	globalOnly, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeUses(t, globalOnly, "shared"); got != "base" {
		t.Errorf("global-only shared nodes = %q, want the global declaration", got)
	}
}

func TestLoadDefinitions_RecordedProjectRootMustStillBeAMarkedTrustedRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		marked  bool
		trusted bool
		untrust bool
	}{
		{name: "a root removed from trusted_project_roots is refused", marked: true, untrust: true},
		{name: "a root whose marker is gone is refused", trusted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "widgets")
			writeFile(t, filepath.Join(root, ".plect", "workflows", "shared.toml"), workflowUsing("shared", "project"))
			if tc.marked {
				markProject(t, root)
			}
			cfg := &Config{BaseDir: t.TempDir()}
			if tc.trusted {
				cfg.TrustedProjectRoots = []string{root}
			}

			_, err := cfg.LoadWorkflows(root)
			if err == nil {
				t.Fatal("LoadWorkflows succeeded, want the recorded root refused rather than another context selected")
			}
			var untrusted *UntrustedProjectError
			if errors.As(err, &untrusted) != tc.untrust {
				t.Errorf("error %v: UntrustedProjectError = %v, want %v", err, !tc.untrust, tc.untrust)
			}
		})
	}
}

func TestLoadDefinitions_ProjectLayerRefusesMachineOwnedKinds(t *testing.T) {
	for _, tc := range []struct {
		kind string
		body string
	}{
		{"resource", "[issue]\nkind = \"resource\"\nname = \"issue\"\nmatch = '^x$'\n"},
		{"channel", "[notify]\nkind = \"channel\"\ntype = \"unix_socket\"\npath = \"/tmp/x.sock\"\nbody = \"x\"\n"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "widgets")
			markProject(t, root)
			writeFile(t, filepath.Join(root, ".plect", tc.kind+".toml"), tc.body)
			cfg := &Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}

			_, err := cfg.LoadWorkflows(root)
			if err == nil || !strings.Contains(err.Error(), tc.kind) {
				t.Fatalf("LoadWorkflows = %v, want a load error naming the %s kind", err, tc.kind)
			}
		})
	}
}

func TestLoadDefinitions_ProjectMarkerIsNotADefinition(t *testing.T) {
	root := filepath.Join(t.TempDir(), "widgets")
	markProject(t, root)
	writeFile(t, filepath.Join(root, ".plect", "workflows", "shared.toml"), workflowUsing("shared", "project"))
	cfg := &Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}

	if _, err := cfg.LoadWorkflows(root); err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
}
