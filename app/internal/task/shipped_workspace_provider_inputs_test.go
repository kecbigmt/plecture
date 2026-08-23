package task

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func loadShippedWorkspaceProviders(t *testing.T) (map[string]config.WorkspaceProviderConfig, []plugins.Mounted) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	catalogRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "plugins")
	manifest, err := plugins.LoadCatalogManifest(catalogRoot)
	if err != nil {
		t.Fatalf("LoadCatalogManifest: %v", err)
	}
	var pluginDirs []string
	var mounted []plugins.Mounted
	for _, rel := range manifest.Plugins {
		dir := filepath.Join(catalogRoot, rel)
		m, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", rel, err)
		}
		mounted = append(mounted, plugins.Mounted{ID: "official/" + rel, Dir: dir, Manifest: m})
		pluginDirs = append(pluginDirs, dir)
	}
	cfg := &config.Config{PluginDirs: pluginDirs, Plugins: mounted}
	provs, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		t.Fatalf("LoadWorkspaceProviders(shipped catalog): %v", err)
	}
	return provs, mounted
}

// providerResolution resolves one shipped provider hook into the values it
// hands its executable, so a parameter declared but never wired into a flag
// fails here instead of silently doing nothing in production.
func providerResolution(t *testing.T, prov config.WorkspaceProviderConfig, mounted []plugins.Mounted, action *lang.Action, env lang.Roots) string {
	t.Helper()
	bins := config.MountedBins{Mounted: mounted, SourcePath: prov.SourcePath}
	eval := lang.Eval{
		Roots: env,
		Bin:   func(ref string) (string, error) { return bins.ResolveBin(ref, prov.Ownership()) },
	}
	if action.Type == lang.ActionShell {
		var parts []string
		for name, bound := range action.Bind {
			value, absent, err := eval.Argument(bound)
			if err != nil {
				t.Fatalf("bind.%s: %v", name, err)
			}
			if absent {
				continue
			}
			parts = append(parts, name+"="+value)
		}
		sort.Strings(parts)
		return strings.Join(parts, "\n")
	}
	execution, err := eval.Exec(action)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return strings.Join(execution.Argv, "\n")
}

func githubProviderEnv(inputs, cleanupInputs map[string]any) lang.Roots {
	return lang.Roots{
		"resource": map[string]any{"id": "https://github.com/acme/widgets/issues/42"},
		"session":  map[string]any{"name": "acme/widgets-42", "inputs": map[string]any{}},
		"inputs":   inputs,
		"config":   map[string]any{"workspace_dirs_root": "/tmp/workspace_dirs"},
		"prev":     map[string]any{},
		"self":     map[string]any{"outputs": map[string]any{"workspace_dir": "/tmp/wd", "branch": "b"}},
		"cleanup":  map[string]any{"inputs": cleanupInputs},
		"force":    false,
	}
}

func TestShippedGithubProvider_ParametersReachTheHooks(t *testing.T) {
	provs, mounted := loadShippedWorkspaceProviders(t)
	prov, ok := provs["official.github.worktree"]
	if !ok {
		t.Fatal("shipped catalog has no official.github.worktree workspace provider")
	}
	inputs := map[string]any{
		"workspace_layout_root": "~/worktrees",
		"issue_branch_template": "work/{number}",
		"tagged_branch_suffix":  "/{tag}",
		"delete_branch_default": "true",
	}
	setup := providerResolution(t, prov, mounted, prov.Setup, githubProviderEnv(inputs, map[string]any{}))
	for _, want := range []string{"~/worktrees", "work/{number}", "/{tag}"} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup does not pass %q:\n%s", want, setup)
		}
	}

	cleanup := providerResolution(t, prov, mounted, prov.Cleanup,
		githubProviderEnv(inputs, map[string]any{"delete_branch": "false"}))
	if !strings.Contains(cleanup, "delete_branch=false") || !strings.Contains(cleanup, "delete_branch_default=true") {
		t.Errorf("cleanup does not carry both the caller's intent and the declared default:\n%s", cleanup)
	}
}

// A workflow that declares no parameters must still resolve: every reference
// declares a default, so the executable keeps owning what an unset one means.
func TestShippedGithubProvider_HooksResolveWithNoParametersDeclared(t *testing.T) {
	provs, mounted := loadShippedWorkspaceProviders(t)
	prov := provs["official.github.worktree"]
	env := githubProviderEnv(map[string]any{}, map[string]any{})
	setup := providerResolution(t, prov, mounted, prov.Setup, env)
	// The flag is still passed, with an empty value the executable reads as
	// "no template declared".
	if !strings.Contains(setup, "--issue-branch-template\n\n") && !strings.HasSuffix(setup, "--issue-branch-template\n") {
		t.Errorf("setup does not pass an empty naming template:\n%q", setup)
	}
	providerResolution(t, prov, mounted, prov.Cleanup, env)
}

// The declared parameters keep their charset restrictions: nothing is spliced
// into a command line any more, but these values still bound what reaches
// github-worktree's own path and template handling.
func TestShippedGithubProvider_ParametersRejectShellMetacharacters(t *testing.T) {
	provs, _ := loadShippedWorkspaceProviders(t)
	prov := provs["official.github.worktree"]
	schema, err := lang.CompileSchema(prov.InputsSchema, prov.ResolvedInputsSchemaPath(), "test:github:inputs")
	if err != nil {
		t.Fatalf("CompileSchema: %v", err)
	}
	if schema == nil {
		t.Fatal("the shipped github workspace provider declares no inputs_schema")
	}
	for _, key := range []string{"workspace_layout_root", "issue_branch_template", "tagged_branch_suffix"} {
		if err := schema.Validate(map[string]any{key: "x' ; touch /tmp/pwned ; echo '"}); err == nil {
			t.Errorf("inputs_schema accepted a %s value containing shell metacharacters", key)
		}
	}
	if err := schema.Validate(map[string]any{"delete_branch_default": "yes"}); err == nil {
		t.Error("inputs_schema accepted a delete_branch_default outside true/false")
	}
	if err := schema.Validate(map[string]any{"unknown_parameter": "x"}); err == nil {
		t.Error("inputs_schema accepted an undeclared parameter")
	}
}
