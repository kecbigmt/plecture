package task

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func mustMount(id, dir string, executables ...plugins.Executable) plugins.Mounted {
	return plugins.Mounted{ID: id, Dir: dir, Manifest: plugins.Manifest{Executables: executables}}
}

func TestResolveBin_ShorthandResolvesSoleExecutable(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/agent/runtime", "/mnt/agent-runtime", plugins.Executable{Name: "agent-runtime", Path: "bin/agent-runtime"})}

	got, err := resolveBin(mounted, "official/agent/runtime")
	if err != nil {
		t.Fatalf("resolveBin: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/agent-runtime", "bin", "agent-runtime")
	if got != want {
		t.Errorf("resolveBin = %q, want %q", got, want)
	}
}

func TestResolveBin_ShorthandAmbiguousWithMultipleExecutables(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "plect-github-provider", Path: "bin/plect-github-provider"},
		plugins.Executable{Name: "plect-github-watcher", Path: "bin/plect-github-watcher"},
	)}

	if _, err := resolveBin(mounted, "official/github"); err == nil {
		t.Fatal("resolveBin: want error for a shorthand reference to a multi-executable plugin, got nil")
	}
}

func TestResolveBin_FullFormDisambiguates(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "plect-github-provider", Path: "bin/plect-github-provider"},
		plugins.Executable{Name: "plect-github-watcher", Path: "bin/plect-github-watcher"},
	)}

	got, err := resolveBin(mounted, "official/github/plect-github-watcher")
	if err != nil {
		t.Fatalf("resolveBin: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/github", "bin", "plect-github-watcher")
	if got != want {
		t.Errorf("resolveBin = %q, want %q", got, want)
	}
}

func TestResolveBin_UnknownPluginID(t *testing.T) {
	if _, err := resolveBin(nil, "official/nope"); err == nil {
		t.Fatal("resolveBin: want error for an unmounted plugin, got nil")
	}
}

func TestResolveBin_MissingCatalogAliasNeverMatches(t *testing.T) {
	// A reference with no "<catalog-alias>/" prefix can never equal or
	// prefix any mounted plugin id (ids always start with an alias).
	mounted := []plugins.Mounted{mustMount("official/agent/runtime", "/mnt/agent-runtime", plugins.Executable{Name: "agent-runtime", Path: "bin/agent-runtime"})}

	if _, err := resolveBin(mounted, "agent-runtime"); err == nil {
		t.Fatal("resolveBin: want error for a reference with no catalog alias, got nil")
	}
}

func TestResolveBin_UnknownExecutableName(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github", plugins.Executable{Name: "plect-github-provider", Path: "bin/plect-github-provider"})}

	if _, err := resolveBin(mounted, "official/github/nope"); err == nil {
		t.Fatal("resolveBin: want error for an unknown executable name, got nil")
	}
}

func TestResolveBin_ZeroExecutablePlugin(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/config-only", "/mnt/config-only")}

	if _, err := resolveBin(mounted, "official/config-only"); err == nil {
		t.Fatal("resolveBin: want error for a plugin with no executables, got nil")
	}
}

// TestResolveBin_NestedPluginCollisionFailsLoud covers the ambiguity the
// design calls out explicitly: a shorter plugin's executable name happens
// to equal the remainder of a reference that also exactly names a longer,
// nested plugin. Neither reading may be silently preferred.
func TestResolveBin_NestedPluginCollisionFailsLoud(t *testing.T) {
	mounted := []plugins.Mounted{
		mustMount("official/agent", "/mnt/agent", plugins.Executable{Name: "runtime", Path: "bin/runtime"}),
		mustMount("official/agent/runtime", "/mnt/agent-runtime", plugins.Executable{Name: "agent-runtime", Path: "bin/agent-runtime"}),
	}

	if _, err := resolveBin(mounted, "official/agent/runtime"); err == nil {
		t.Fatal("resolveBin: want error for a reference readable as two different plugin/executable pairs, got nil")
	}
}

// TestRender_BinHelperResolvesThroughSessionPlugins exercises {{bin ...}}
// end-to-end through render, the same path a setup/cleanup hook string goes
// through — not just the unit-level resolveBin.
func TestRender_BinHelperResolvesThroughSessionPlugins(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/agent/runtime", "/mnt/agent-runtime", plugins.Executable{Name: "agent-runtime", Path: "bin/agent-runtime"})}

	out, err := render(`{{bin "official/agent/runtime"}} launch`, RenderContext{
		Session: SessionVars{Plugins: mounted},
	})
	if err != nil {
		t.Fatalf("render: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/agent-runtime", "bin", "agent-runtime") + " launch"
	if out != want {
		t.Errorf("render = %q, want %q", out, want)
	}
}

func TestRender_BinHelperUnresolvedFailsLoud(t *testing.T) {
	if _, err := render(`{{bin "official/nope"}}`, RenderContext{Session: SessionVars{}}); err == nil {
		t.Fatal("render: want error for an unresolvable {{bin ...}} reference, got nil")
	}
}
