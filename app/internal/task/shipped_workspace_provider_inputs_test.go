package task

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
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

// TestShippedGithubProvider_ParametersReachTheHooks renders the shipped
// GitHub workspace provider's hooks with every declared parameter set, so a
// parameter that is declared but never wired into the executable's flags
// fails here instead of silently doing nothing in production.
func TestShippedGithubProvider_ParametersReachTheHooks(t *testing.T) {
	provs, mounted := loadShippedWorkspaceProviders(t)
	prov, ok := provs["github"]
	if !ok {
		t.Fatal("shipped catalog has no github workspace provider")
	}
	vars := WorkflowHookVars{
		ResourceID:        "https://github.com/acme/widgets/issues/42",
		SessionName:       "acme/widgets-42",
		WorkspaceDirsRoot: "/tmp/workspace_dirs",
		Plugins:           mounted,
		SourcePath:        prov.SourcePath,
		Inputs: map[string]any{
			"workspace_layout_root": "~/worktrees",
			"issue_branch_template": "work/{number}",
			"tagged_branch_suffix":  "/{tag}",
			"delete_branch_default": "true",
		},
	}
	setup, err := renderWorkflowHook(prov.Setup, vars, map[string]any{}, nil, "missingkey=error")
	if err != nil {
		t.Fatalf("setup render: %v", err)
	}
	for _, want := range []string{"--workspace-layout-root '~/worktrees'", "--issue-branch-template 'work/{number}'", "--tagged-branch-suffix '/{tag}'"} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup does not pass %s:\n%s", want, setup)
		}
	}

	self := map[string]any{"workspace_dir": "/tmp/wd", "branch": "b"}
	vars.CleanupInputs = map[string]string{"delete_branch": "false"}
	cleanup, err := renderWorkflowHook(prov.Cleanup, vars, nil, self, "missingkey=zero")
	if err != nil {
		t.Fatalf("cleanup render: %v", err)
	}
	if !strings.Contains(cleanup, "--delete-branch='false'") || !strings.Contains(cleanup, "--delete-branch-default='true'") {
		t.Errorf("cleanup does not carry both the caller's intent and the declared default:\n%s", cleanup)
	}
}

// A workflow that declares no parameters must still render: every reference
// goes through `get`, so the executable keeps owning the defaults.
func TestShippedGithubProvider_HooksRenderWithNoParametersDeclared(t *testing.T) {
	provs, mounted := loadShippedWorkspaceProviders(t)
	prov := provs["github"]
	vars := WorkflowHookVars{
		ResourceID:        "https://github.com/acme/widgets/issues/42",
		SessionName:       "acme/widgets-42",
		WorkspaceDirsRoot: "/tmp/workspace_dirs",
		Plugins:           mounted,
		SourcePath:        prov.SourcePath,
	}
	setup, err := renderWorkflowHook(prov.Setup, vars, map[string]any{}, nil, "missingkey=error")
	if err != nil {
		t.Fatalf("setup render: %v", err)
	}
	if !strings.Contains(setup, "--issue-branch-template ''") {
		t.Errorf("setup does not pass an empty naming template:\n%s", setup)
	}
	if _, err := renderWorkflowHook(prov.Cleanup, vars, nil, map[string]any{"workspace_dir": "/tmp/wd", "branch": "b"}, "missingkey=zero"); err != nil {
		t.Fatalf("cleanup render: %v", err)
	}
}

// The declared parameters must reject the shell metacharacters that would
// otherwise break out of the single-quoted splice points in the hooks above.
func TestShippedGithubProvider_ParametersRejectShellMetacharacters(t *testing.T) {
	provs, _ := loadShippedWorkspaceProviders(t)
	prov := provs["github"]
	schema, err := CompileSchema(prov.InputsSchema, prov.ResolvedInputsSchemaPath(), "test:github:inputs")
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
