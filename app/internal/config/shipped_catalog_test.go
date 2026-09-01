package config

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/version"
)

// loadShippedCatalog mounts this repository's own official catalog
// (plugins/catalog.toml plus the plugin directories it lists) under alias,
// the same way `plect catalog add <alias> ...` / `plect plugin add
// <alias>/<path>` would mount it for a real user — so a test using this
// helper with an alias other than "official" catches a shipped {{bin ...}}
// reference that only happens to resolve under this repository's own
// catalog example alias.
func loadShippedCatalog(t *testing.T, alias string) *Config {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	catalogRoot := filepath.Join(repoRoot, "plugins")

	manifest, err := plugins.LoadCatalogManifest(catalogRoot)
	if err != nil {
		t.Fatalf("LoadCatalogManifest(catalog root): %v", err)
	}
	if len(manifest.Plugins) == 0 {
		t.Fatal("shipped catalog.toml lists no plugins")
	}

	var pluginDirs []string
	var mounted []plugins.Mounted
	for _, rel := range manifest.Plugins {
		dir := filepath.Join(catalogRoot, rel)
		m, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", rel, err)
		}
		ok, err := plugins.AtLeast(version.Current, m.PlectMinVersion)
		if err != nil {
			t.Fatalf("plugin %s: plect_min_version %q: %v", rel, m.PlectMinVersion, err)
		}
		if !ok {
			t.Errorf("plugin %s requires plect >= %s, running %s", rel, m.PlectMinVersion, version.Current)
		}
		pluginDirs = append(pluginDirs, dir)
		mounted = append(mounted, plugins.Mounted{ID: alias + "/" + rel, Dir: dir, Manifest: m})
	}

	return &Config{PluginDirs: pluginDirs, Plugins: mounted}
}

// TestShippedCatalog_LoadsAndCompiles guards this repository's own official
// catalog against a broken manifest or an unparseable task/channel file.
// This is the only place that exercises the catalog end to end: a plugin
// author otherwise only discovers a typo the first time a user runs `plect
// plugin add`.
func TestShippedCatalog_LoadsAndCompiles(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	docs, tasks, err := cfg.LoadTaskDeclarations("")
	if err != nil {
		t.Fatalf("LoadTaskDeclarations(shipped catalog): %v", err)
	}
	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	if _, err := cfg.LoadWorkspaceProviders(); err != nil {
		t.Fatalf("LoadWorkspaceProviders(shipped catalog): %v", err)
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows(shipped catalog): %v", err)
	}
	if err := cfg.ValidateTaskDocuments(docs, observers, workflows); err != nil {
		t.Fatalf("ValidateTaskDocuments(shipped catalog): %v", err)
	}

	// The addresses are spelled out because the address is the thing under
	// test: a shipped declaration answers to its catalog path, so a plugin
	// moving between catalog directories changes how config selects it.
	for _, address := range []string{
		"official.tmux.pane",
		"official.claude.claude_initial_prompt",
		"official.claude.runtime",
		"official.codex.codex",
		"official.codex.codex_initial_prompt",
		"official.codex.exec_runtime",
		"official.github.gh_guard",
		"official.slack.slack_thread",
	} {
		if _, ok := tasks[address]; !ok {
			t.Errorf("shipped catalog effect %q not found", address)
		}
	}
	for _, address := range []string{
		"official.github.work",
		"official.github.review",
		"official.github.investigate",
		"official.github.respond",
		"official.okf.pursue_goal",
		"official.okf.goal_review",
	} {
		if _, ok := docs[address]; !ok {
			t.Errorf("shipped catalog task document %q not found", address)
		}
	}
	for _, address := range []string{
		"official.codex.terminal_submit",
		"official.claude.delivery",
		"official.codex.exec_delivery",
		"official.slack.slack",
	} {
		if _, ok := channels[address]; !ok {
			t.Errorf("shipped catalog channel %q not found", address)
		}
	}
}

// TestShippedCatalog_LoadsUnderArbitraryAlias re-runs the same load under an
// alias other than "official": every {{bin ...}} reference in shipped
// config must resolve under any catalog alias a user chooses, per
// docs/design/plugin-packaging.md's Plugin-local {{bin}} resolution
// section — a fully-qualified self-reference that happens to spell out
// "official" would pass TestShippedCatalog_LoadsAndCompiles and still be
// wrong.
func TestShippedCatalog_LoadsUnderArbitraryAlias(t *testing.T) {
	cfg := loadShippedCatalog(t, "acme")
	if _, _, err := cfg.LoadTaskDeclarations(""); err != nil {
		t.Fatalf("LoadTaskDeclarations(shipped catalog, alias %q): %v", "acme", err)
	}
	if _, err := cfg.LoadChannels(); err != nil {
		t.Fatalf("LoadChannels(shipped catalog, alias %q): %v", "acme", err)
	}
	if _, err := cfg.LoadWorkspaceProviders(); err != nil {
		t.Fatalf("LoadWorkspaceProviders(shipped catalog, alias %q): %v", "acme", err)
	}
	if _, err := cfg.LoadResourceDefs(); err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog, alias %q): %v", "acme", err)
	}
}

func TestShippedCatalog_SlackAdapterServiceRequiresBotTokenOnly(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")

	var svc *plugins.Service
	for _, m := range cfg.Plugins {
		for i, s := range m.Manifest.Services {
			if s.Name == "slack-adapter" {
				svc = &m.Manifest.Services[i]
			}
		}
	}
	if svc == nil {
		t.Fatal("no shipped plugin declares a slack-adapter service")
	}

	if len(svc.RequiredEnv) != 1 || svc.RequiredEnv[0] != "SLACK_BOT_TOKEN" {
		t.Errorf("slack-adapter service required_env = %v, want bot-token-only outbound startup", svc.RequiredEnv)
	}
}

// TestShippedCatalog_GhAppGuardIsRunScoped guards against gh_app_guard.toml
// reverting to session scope: its output is a directory under the current
// runtime's TMPDIR, meaningful only while that runtime's PATH points at it,
// so a session-scoped declaration would survive a container replacement in
// state but not on disk, leaving a resumed session with a PATH entry that no
// longer exists.
func TestShippedCatalog_GhAppGuardIsRunScoped(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	_, tasks, err := cfg.LoadTaskDeclarations("")
	if err != nil {
		t.Fatalf("LoadTaskDeclarations(shipped catalog): %v", err)
	}
	def, ok := tasks["official.github.gh_app_guard"]
	if !ok {
		t.Fatal("shipped catalog effect \"official.github.gh_app_guard\" not found")
	}
	if got := def.EffectiveScope(); got != TaskScopeRun {
		t.Errorf("official.github.gh_app_guard scope = %q, want %q", got, TaskScopeRun)
	}
}
