package task

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// mountShippedPlugin mounts one shipped plugin directory (repo-root-relative,
// e.g. "plugins/github") under alias, mirroring loadShippedCatalogTasks'
// id-construction but parameterized on the alias so a caller can register the
// catalog under something other than "official".
func mountShippedPlugin(t *testing.T, repoRoot, alias, rel string) plugins.Mounted {
	t.Helper()
	dir := filepath.Join(repoRoot, rel)
	m, err := plugins.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest(%s): %v", rel, err)
	}
	id := alias + "/" + strings.TrimPrefix(rel, "plugins/")
	return plugins.Mounted{ID: id, Dir: dir, Manifest: m}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// TestShippedGithubOkf_BinReferencesResolveUnderArbitraryAlias is the
// acceptance test for plugin-local {{bin}} resolution (docs/design/
// plugin-packaging.md, "Plugin-local {{bin}} resolution"): every shipped
// github and okf resource/provider/task hook must resolve its own plugin's
// executables regardless of which alias the operator registered the catalog
// under. "official" is deliberately avoided — a regression that silently
// re-depends on that one specific alias would otherwise pass unnoticed.
func TestShippedGithubOkf_BinReferencesResolveUnderArbitraryAlias(t *testing.T) {
	const alias = "acme-mirror"
	repoRoot := repoRootForTest(t)

	github := mountShippedPlugin(t, repoRoot, alias, "plugins/github")
	okf := mountShippedPlugin(t, repoRoot, alias, "plugins/okf")
	mounted := []plugins.Mounted{github, okf}

	cfg := &config.Config{
		PluginDirs: []string{github.Dir, okf.Dir},
		Plugins:    mounted,
	}

	resDefs, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs: %v", err)
	}
	provDefs, err := cfg.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	taskDefs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	if len(resDefs) == 0 || len(provDefs) == 0 || len(taskDefs) == 0 {
		t.Fatalf("expected github+okf to declare resources, providers, and tasks; got %d/%d/%d", len(resDefs), len(provDefs), len(taskDefs))
	}

	for id, def := range resDefs {
		if def.Observe != "" {
			ctx := RenderContext{
				Session:    SessionVars{ResourceID: "owner:test", Plugins: mounted},
				SourcePath: def.SourcePath,
			}
			if _, err := render(def.Observe, ctx); err != nil {
				t.Errorf("resource %q observe: %v", id, err)
			}
		}
		if def.Finalize != "" {
			data := finalizeTemplateData{
				ResourceID: "owner:test",
				Revision:   "abc123",
				JudgesJSON: "[]",
				Plugins:    mounted,
				SourcePath: def.SourcePath,
			}
			if _, err := renderFinalize(def.Finalize, data); err != nil {
				t.Errorf("resource %q finalize: %v", id, err)
			}
		}
	}

	for id, prov := range provDefs {
		vars := WorkflowHookVars{
			ResourceID:    "owner:test",
			SessionName:   "test-session",
			WorkdirsRoot:  "/tmp/workdirs",
			SessionInputs: map[string]any{},
			Plugins:       mounted,
			SourcePath:    prov.SourcePath,
		}
		if prov.Setup != "" {
			if _, err := renderWorkflowHook(prov.Setup, vars, map[string]any{}, nil, "missingkey=error"); err != nil {
				t.Errorf("provider %q setup: %v", id, err)
			}
		}
		if prov.Cleanup != "" {
			self := map[string]any{"workdir": "/tmp/wd", "branch": "b"}
			if _, err := renderWorkflowHook(prov.Cleanup, vars, nil, self, "missingkey=zero"); err != nil {
				t.Errorf("provider %q cleanup: %v", id, err)
			}
		}
		if prov.Subscribe != "" {
			subVars := SubscribeHookVars{ResourceID: "owner:test", SessionName: "test-session", Plugins: mounted, SourcePath: prov.SourcePath}
			if _, err := renderSubscribeHook(prov.Subscribe, subVars); err != nil {
				t.Errorf("provider %q subscribe: %v", id, err)
			}
		}
	}

	session := SessionVars{Name: "test-session", ResourceID: "owner:test", WorkdirPath: "/tmp/wd", Plugins: mounted}
	inputs := map[string]any{"owner": "acme", "assignees": ""}
	for id, def := range taskDefs {
		ctx := RenderContext{
			Self:       map[string]any{},
			Prev:       map[string]any{},
			Inputs:     inputs,
			Session:    session,
			SourcePath: def.SourcePath,
		}
		if def.Setup != "" {
			if _, err := render(def.Setup, ctx); err != nil {
				t.Errorf("task %q setup: %v", id, err)
			}
		}
		if def.Cleanup != "" {
			if _, err := renderCleanup(def.Cleanup, ctx); err != nil {
				t.Errorf("task %q cleanup: %v", id, err)
			}
		}
	}
}
