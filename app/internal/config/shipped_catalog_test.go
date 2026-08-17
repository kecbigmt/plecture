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
	tasks, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions(shipped catalog): %v", err)
	}
	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	if _, err := cfg.LoadWorkspaceProviders(); err != nil {
		t.Fatalf("LoadWorkspaceProviders(shipped catalog): %v", err)
	}
	if _, err := cfg.LoadResourceDefs(); err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}

	for _, id := range []string{"tmux", "claude_initial_prompt", "claude", "codex", "codex_initial_prompt", "codex_exec", "gh_guard"} {
		if _, ok := tasks[id]; !ok {
			t.Errorf("shipped catalog task %q not found", id)
		}
	}
	for _, id := range []string{"terminal_submit", "claude", "codex_exec", "slack"} {
		if _, ok := channels[id]; !ok {
			t.Errorf("shipped catalog channel %q not found", id)
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
	if _, err := cfg.LoadTaskDefinitions(""); err != nil {
		t.Fatalf("LoadTaskDefinitions(shipped catalog, alias %q): %v", "acme", err)
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

// TestShippedCatalog_WorkflowEventChannelsResolve guards every shipped
// workflow's [[event.channel]] against the full mounted catalog's channels.
// LoadWorkflows never calls ValidateWorkflowChannels, so a `uses` value no
// plugin actually ships (e.g. a channel/task naming collision) only
// surfaced at `plect workflow show`/`up` time, not `plugin add` or
// `workflow list`.
func TestShippedCatalog_WorkflowEventChannelsResolve(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows(shipped catalog): %v", err)
	}

	checked := 0
	for id, wf := range workflows {
		if len(wf.Event.Channel) == 0 {
			continue
		}
		checked++
		if err := ValidateWorkflowChannels(wf, channels); err != nil {
			t.Errorf("workflow %q: %v", id, err)
		}
	}
	if checked == 0 {
		t.Fatal("no shipped workflow declares event channels; this test is checking nothing")
	}
}

// TestShippedCatalog_SlackAdapterServiceRequiresAllStartupCredentials guards
// the slack-adapter [[services]] declaration in the slack plugin's
// plugin.toml against a partial required_env list: slack-adapter's
// cmd/slack-adapter/main.go exits(1) at startup if any of the bot token,
// app token, or channel id is unset, so a required_env list missing one of
// them would let the plugin service supervisor start (and then crash-loop)
// slack-adapter instead of leaving it inert, the way an unconfigured
// service should stay. This regression guards the specific gap a review
// caught: required_env originally listed only the two tokens, not the
// channel id.
func TestShippedCatalog_SlackAdapterServiceRequiresAllStartupCredentials(t *testing.T) {
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

	want := []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CHANNEL_ID"}
	for _, name := range want {
		found := false
		for _, e := range svc.RequiredEnv {
			if e == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("slack-adapter service required_env = %v, missing %q (main.go exits at startup without it)", svc.RequiredEnv, name)
		}
	}
}
