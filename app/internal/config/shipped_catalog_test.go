package config

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
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

// renderQueryArgv resolves a query means against a fixed inputs root, the
// same way the population evaluator would, so these tests exercise the
// exact argv the shipped catalog produces rather than the TOML source text.
func renderQueryArgv(t *testing.T, cfg *Config, def ResourceDef, action *lang.Action, inputs map[string]any) []string {
	t.Helper()
	eval := lang.Eval{
		Roots: lang.Roots{"inputs": inputs},
		Bin: func(ref string) (string, error) {
			return cfg.binResolver(def.SourcePath).ResolveBin(ref, def.Ownership())
		},
	}
	execution, err := eval.Run("", action, nil)
	if err != nil {
		t.Fatalf("render query action: %v", err)
	}
	return execution.Argv
}

func TestShippedCatalog_GitHubPullQueryInvokesDocumentedFlags(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}
	def, ok := observers["official.github.pull_request"]
	if !ok {
		t.Fatal("shipped catalog resource observer \"official.github.pull_request\" not found")
	}
	if def.Query == nil {
		t.Fatal("official.github.pull_request declares no query face")
	}
	if def.Query.Poll == nil {
		t.Fatal("official.github.pull_request's query declares no poll means")
	}
	if def.Query.Subscribe == nil {
		t.Fatal("official.github.pull_request's query declares no subscribe means")
	}

	inputs := map[string]any{
		"repositories": []any{"acme/widgets"},
		"labels":       []any{"agent-review"},
		"state":        "open",
		"draft":        false,
	}

	pollArgv := renderQueryArgv(t, cfg, def, def.Query.Poll, inputs)
	assertGitHubQueryArgv(t, pollArgv, "github-issue-pr", "query-pulls")

	subscribeArgv := renderQueryArgv(t, cfg, def, def.Query.Subscribe, inputs)
	assertGitHubQueryArgv(t, subscribeArgv, "github-webhook-receiver", "subscribe-pulls")
}

func assertGitHubQueryArgv(t *testing.T, argv []string, wantBin, wantSubcommand string) {
	t.Helper()
	if len(argv) == 0 {
		t.Fatal("argv is empty")
	}
	if !strings.HasSuffix(argv[0], wantBin) {
		t.Errorf("argv[0] = %q, want it to resolve %q", argv[0], wantBin)
	}
	if len(argv) < 2 || argv[1] != wantSubcommand {
		t.Errorf("argv[1] = %v, want %q", argv, wantSubcommand)
	}
	flags := map[string]string{}
	for i := 2; i+1 < len(argv); i += 2 {
		flags[argv[i]] = argv[i+1]
	}
	var repositories, labels []string
	if err := json.Unmarshal([]byte(flags["--repositories"]), &repositories); err != nil {
		t.Fatalf("--repositories %q is not JSON: %v", flags["--repositories"], err)
	}
	if len(repositories) != 1 || repositories[0] != "acme/widgets" {
		t.Errorf("--repositories = %v, want [acme/widgets]", repositories)
	}
	if err := json.Unmarshal([]byte(flags["--labels"]), &labels); err != nil {
		t.Fatalf("--labels %q is not JSON: %v", flags["--labels"], err)
	}
	if len(labels) != 1 || labels[0] != "agent-review" {
		t.Errorf("--labels = %v, want [agent-review]", labels)
	}
	if flags["--state"] != "open" {
		t.Errorf("--state = %q, want %q", flags["--state"], "open")
	}
	if flags["--draft"] != "false" {
		t.Errorf("--draft = %q, want %q", flags["--draft"], "false")
	}
}

func TestShippedCatalog_GitHubPullQueryItemSchemaBoundary(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}
	def, ok := observers["official.github.pull_request"]
	if !ok {
		t.Fatal("shipped catalog resource observer \"official.github.pull_request\" not found")
	}
	required, _ := def.Query.ItemSchema["required"].([]any)
	if len(required) != 1 || required[0] != "resource" {
		t.Errorf("query.item_schema.required = %v, want exactly [\"resource\"]", required)
	}
	itemProps, _ := def.Query.ItemSchema["properties"].(map[string]any)
	stateProps, _ := def.StateSchema["properties"].(map[string]any)
	for prop := range itemProps {
		if _, clash := stateProps[prop]; clash {
			t.Errorf("query item_schema property %q also appears in state_schema", prop)
		}
	}
}

func TestShippedCatalog_SlackThreadQueryInvokesDocumentedFlags(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}
	def, ok := observers["official.slack.thread"]
	if !ok {
		t.Fatal("shipped catalog resource observer \"official.slack.thread\" not found")
	}
	if def.Query == nil {
		t.Fatal("official.slack.thread declares no query face")
	}
	if def.Query.Poll != nil {
		t.Error("official.slack.thread declares a poll means, but a mention appearance is not enumerable")
	}
	if def.Query.Subscribe == nil {
		t.Fatal("official.slack.thread's query declares no subscribe means")
	}

	inputs := map[string]any{
		"base_url":    "http://127.0.0.1:7890",
		"channel_ids": []any{"C01234567"},
	}
	argv := renderQueryArgv(t, cfg, def, def.Query.Subscribe, inputs)
	if len(argv) == 0 || !strings.HasSuffix(argv[0], "slack-adapter") {
		t.Fatalf("argv[0] = %v, want it to resolve slack-adapter", argv)
	}
	if len(argv) < 3 || argv[1] != "subscribe" || argv[2] != "unbound-mentions" {
		t.Errorf("argv = %v, want it to start with subscribe unbound-mentions", argv)
	}
	flags := map[string]string{}
	for i := 3; i+1 < len(argv); i += 2 {
		flags[argv[i]] = argv[i+1]
	}
	if flags["--base-url"] != "http://127.0.0.1:7890" {
		t.Errorf("--base-url = %q, want %q", flags["--base-url"], "http://127.0.0.1:7890")
	}
	var channelIDs []string
	if err := json.Unmarshal([]byte(flags["--channel-ids"]), &channelIDs); err != nil {
		t.Fatalf("--channel-ids %q is not JSON: %v", flags["--channel-ids"], err)
	}
	if len(channelIDs) != 1 || channelIDs[0] != "C01234567" {
		t.Errorf("--channel-ids = %v, want [C01234567]", channelIDs)
	}

	required, _ := def.Query.ItemSchema["required"].([]any)
	if len(required) != 1 || required[0] != "resource" {
		t.Errorf("query.item_schema.required = %v, want exactly [\"resource\"]", required)
	}
	itemProps, _ := def.Query.ItemSchema["properties"].(map[string]any)
	stateProps, _ := def.StateSchema["properties"].(map[string]any)
	for prop := range itemProps {
		if _, clash := stateProps[prop]; clash {
			t.Errorf("query item_schema property %q also appears in state_schema", prop)
		}
	}
}

// The workflow here is a throwaway test fixture, not shipped config:
// populations declarations are deployment policy, out of scope for this
// plugin.
func TestShippedCatalog_WorkflowPopulationResolvesGitHubPullQuery(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	cfg.BaseDir = t.TempDir()
	writeFile(t, filepath.Join(cfg.BaseDir, "agent.toml"), `
[agent]
kind               = "workflow"
workspace_provider = "official.github.worktree"

[[agent.populations]]
name              = "dispatch"
resource_observer = "official.github.pull_request"
uses              = ["poll"]
poll_every        = "1m"
auto_down         = true
auto_destroy      = true

[agent.populations.query]
repositories = ["acme/widgets"]
labels       = ["agent-review"]
state        = "open"
draft        = false
`)
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	populations := workflows["agent"].Populations
	if len(populations) != 1 || populations[0].ResourceObserver != "official.github.pull_request" {
		t.Fatalf("populations = %+v", populations)
	}
}

// expire_after (not poll_every) is required here because
// official.slack.thread declares no poll means.
func TestShippedCatalog_WorkflowPopulationResolvesSlackThreadQuery(t *testing.T) {
	cfg := loadShippedCatalog(t, "official")
	cfg.BaseDir = t.TempDir()
	writeFile(t, filepath.Join(cfg.BaseDir, "ops.toml"), `
[ops]
kind               = "workflow"
workspace_provider = "official.slack.thread_workspace"

[[ops.populations]]
name              = "mentions"
resource_observer = "official.slack.thread"
uses              = ["subscribe"]
expire_after      = "8h"
auto_down         = true
auto_destroy      = true

[ops.populations.query]
base_url    = "http://127.0.0.1:7890"
channel_ids = ["C01234567"]
`)
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	populations := workflows["ops"].Populations
	if len(populations) != 1 || populations[0].ResourceObserver != "official.slack.thread" {
		t.Fatalf("populations = %+v", populations)
	}
}
