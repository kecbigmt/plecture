package task

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func mustMount(id, dir string, executables ...plugins.Executable) plugins.Mounted {
	return plugins.Mounted{ID: id, Dir: dir, Manifest: plugins.Manifest{Executables: executables}}
}

// TestRender_BinHelperResolvesThroughSessionPlugins exercises {{bin ...}}
// end-to-end through render, the same path a setup/cleanup hook string goes
// through — not just the unit-level plugins.ResolveBin.
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

// TestRender_BinHelperResolvesPluginLocalBareName exercises the plugin-local
// {{bin "<name>"}} reading end-to-end through render, using
// RenderContext.SourcePath the way task setup/cleanup templates do.
func TestRender_BinHelperResolvesPluginLocalBareName(t *testing.T) {
	mounted := []plugins.Mounted{
		mustMount("some-alias/github", "/mnt/some-alias/github", plugins.Executable{Name: "plect-github-provider", Path: "bin/plect-github-provider"}),
	}
	sourcePath := filepath.Join("/mnt/some-alias/github", "providers", "github.toml")

	out, err := render(`{{bin "plect-github-provider"}} setup`, RenderContext{
		Session:    SessionVars{Plugins: mounted},
		SourcePath: sourcePath,
	})
	if err != nil {
		t.Fatalf("render: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/some-alias/github", "bin", "plect-github-provider") + " setup"
	if out != want {
		t.Errorf("render = %q, want %q", out, want)
	}
}
