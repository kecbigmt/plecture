package plugins

import (
	"path/filepath"
	"testing"
)

func mustMount(id, dir string, executables ...Executable) Mounted {
	return Mounted{ID: id, Dir: dir, Manifest: Manifest{Executables: executables}}
}

func TestResolveBin_ShorthandResolvesSoleExecutable(t *testing.T) {
	mounted := []Mounted{mustMount("official/agent/runtime", "/mnt/agent-runtime", Executable{Name: "agent-runtime", Path: "bin/agent-runtime"})}

	got, err := ResolveBin(mounted, "", "official/agent/runtime")
	if err != nil {
		t.Fatalf("ResolveBin: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/agent-runtime", "bin", "agent-runtime")
	if got != want {
		t.Errorf("ResolveBin = %q, want %q", got, want)
	}
}

func TestResolveBin_ShorthandAmbiguousWithMultipleExecutables(t *testing.T) {
	mounted := []Mounted{mustMount("official/github", "/mnt/github",
		Executable{Name: "github-worktree", Path: "bin/github-worktree"},
		Executable{Name: "plect-github-watcher", Path: "bin/plect-github-watcher"},
	)}

	if _, err := ResolveBin(mounted, "", "official/github"); err == nil {
		t.Fatal("ResolveBin: want error for a shorthand reference to a multi-executable plugin, got nil")
	}
}

func TestResolveBin_FullFormDisambiguates(t *testing.T) {
	mounted := []Mounted{mustMount("official/github", "/mnt/github",
		Executable{Name: "github-worktree", Path: "bin/github-worktree"},
		Executable{Name: "plect-github-watcher", Path: "bin/plect-github-watcher"},
	)}

	got, err := ResolveBin(mounted, "", "official/github/plect-github-watcher")
	if err != nil {
		t.Fatalf("ResolveBin: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/github", "bin", "plect-github-watcher")
	if got != want {
		t.Errorf("ResolveBin = %q, want %q", got, want)
	}
}

func TestResolveBin_UnknownPluginID(t *testing.T) {
	if _, err := ResolveBin(nil, "", "official/nope"); err == nil {
		t.Fatal("ResolveBin: want error for an unmounted plugin, got nil")
	}
}

func TestResolveBin_BareNameWithNoSourcePathFailsLoud(t *testing.T) {
	// A bare executable name resolves only against the containing plugin
	// (found from sourcePath); an empty sourcePath has no containing plugin
	// to resolve against, regardless of what mounted declares.
	mounted := []Mounted{mustMount("official/agent/runtime", "/mnt/agent-runtime", Executable{Name: "agent-runtime", Path: "bin/agent-runtime"})}

	if _, err := ResolveBin(mounted, "", "agent-runtime"); err == nil {
		t.Fatal("ResolveBin: want error for a bare name with no source file to derive a containing plugin from, got nil")
	}
}

// TestResolveBin_PluginLocalBareNameResolvesWithinContainingPlugin covers
// the plugin-local reading: a bare executable name inside a file mounted
// from a plugin resolves against that plugin's own [[executables]], with no
// alias in the reference at all.
func TestResolveBin_PluginLocalBareNameResolvesWithinContainingPlugin(t *testing.T) {
	mounted := []Mounted{
		mustMount("some-alias/github", "/mnt/some-alias/github", Executable{Name: "github-worktree", Path: "bin/github-worktree"}),
	}
	sourcePath := filepath.Join("/mnt/some-alias/github", "resources", "github.toml")

	got, err := ResolveBin(mounted, sourcePath, "github-worktree")
	if err != nil {
		t.Fatalf("ResolveBin: unexpected error: %v", err)
	}
	want := filepath.Join("/mnt/some-alias/github", "bin", "github-worktree")
	if got != want {
		t.Errorf("ResolveBin = %q, want %q", got, want)
	}
}

// TestResolveBin_PluginLocalBareNameIgnoresOtherPlugins proves plugin-local
// resolution never reaches across plugin boundaries even when another
// mounted plugin happens to declare a same-named executable: shipped
// content may only ever reach its own plugin's executables this way.
func TestResolveBin_PluginLocalBareNameIgnoresOtherPlugins(t *testing.T) {
	mounted := []Mounted{
		mustMount("official/github", "/mnt/official/github", Executable{Name: "watcher", Path: "bin/watcher"}),
		mustMount("official/okf", "/mnt/official/okf", Executable{Name: "okf-goal", Path: "bin/okf-goal"}),
	}
	sourcePath := filepath.Join("/mnt/official/okf", "resources", "okf_goal.toml")

	if _, err := ResolveBin(mounted, sourcePath, "watcher"); err == nil {
		t.Fatal("ResolveBin: want error for a bare name declared by a different plugin than the containing one, got nil")
	}
}

func TestResolveBin_UnknownExecutableName(t *testing.T) {
	mounted := []Mounted{mustMount("official/github", "/mnt/github", Executable{Name: "github-worktree", Path: "bin/github-worktree"})}

	if _, err := ResolveBin(mounted, "", "official/github/nope"); err == nil {
		t.Fatal("ResolveBin: want error for an unknown executable name, got nil")
	}
}

func TestResolveBin_ZeroExecutablePlugin(t *testing.T) {
	mounted := []Mounted{mustMount("official/config-only", "/mnt/config-only")}

	if _, err := ResolveBin(mounted, "", "official/config-only"); err == nil {
		t.Fatal("ResolveBin: want error for a plugin with no executables, got nil")
	}
}

// TestResolveBin_NestedPluginCollisionFailsLoud covers the ambiguity the
// design calls out explicitly: a shorter plugin's executable name happens
// to equal the remainder of a reference that also exactly names a longer,
// nested plugin. Neither reading may be silently preferred.
func TestResolveBin_NestedPluginCollisionFailsLoud(t *testing.T) {
	mounted := []Mounted{
		mustMount("official/agent", "/mnt/agent", Executable{Name: "runtime", Path: "bin/runtime"}),
		mustMount("official/agent/runtime", "/mnt/agent-runtime", Executable{Name: "agent-runtime", Path: "bin/agent-runtime"}),
	}

	if _, err := ResolveBin(mounted, "", "official/agent/runtime"); err == nil {
		t.Fatal("ResolveBin: want error for a reference readable as two different plugin/executable pairs, got nil")
	}
}

func TestBinRefs_FindsBareAndPipedReferences(t *testing.T) {
	got := BinRefs(`{{bin "official/agent/runtime/plect-agent-activity"}} claude working` + "\n" +
		`{{bin "github-watcher" | shellQuote}}` + "\n" +
		`{{get .Inputs "model" ""}}`)
	want := []string{"official/agent/runtime/plect-agent-activity", "github-watcher"}
	if len(got) != len(want) {
		t.Fatalf("BinRefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BinRefs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBinRefs_NoReferencesReturnsNil(t *testing.T) {
	if got := BinRefs(`plain shell, no templating here`); got != nil {
		t.Errorf("BinRefs = %v, want nil", got)
	}
}

// TestBinRefs_UnparsableTemplateReturnsNilNotError proves an unrecognized
// function name degrades this scan silently rather than failing it — see
// binScanFuncs's doc comment for why.
func TestBinRefs_UnparsableTemplateReturnsNilNotError(t *testing.T) {
	if got := BinRefs(`{{someFutureFunc "x"}}`); got != nil {
		t.Errorf("BinRefs = %v, want nil", got)
	}
}
