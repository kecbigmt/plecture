package task

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// loadShippedCatalogTasks loads this repository's own official catalog
// (plugins/catalog.toml plus the plugin directories it lists) the same way
// a real config load would, returning every shipped task definition plus
// the mounted-plugin list `{{bin ...}}` resolves against.
func loadShippedCatalogTasks(t *testing.T) (map[string]config.TaskDefinition, []plugins.Mounted) {
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

	var pluginDirs []string
	var mounted []plugins.Mounted
	for _, rel := range manifest.Plugins {
		dir := filepath.Join(catalogRoot, rel)
		m, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", rel, err)
		}
		id := "official/" + rel
		mounted = append(mounted, plugins.Mounted{ID: id, Dir: dir, Manifest: m})
		pluginDirs = append(pluginDirs, dir)
	}

	cfg := &config.Config{PluginDirs: pluginDirs, Plugins: mounted}
	tasks, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions(shipped catalog): %v", err)
	}
	return tasks, mounted
}

// TestShippedCatalog_TasksRender guards this repository's own official
// catalog against a broken template: a `{{bin ...}}` reference that doesn't
// resolve, or a `{{...}}` action that fails to parse, would otherwise only
// surface the first time a real session runs the task. A kitchen-sink
// context supplies every key any shipped task's setup/cleanup/health probe
// references, so this exercises actual rendering, not just TOML decoding.
func TestShippedCatalog_TasksRender(t *testing.T) {
	tasks, mounted := loadShippedCatalogTasks(t)

	session := SessionVars{
		Name:             "test-session",
		ResourceID:       "owner:test",
		WorkspaceDirPath: "/tmp/test-workdir",
		Plugins:          mounted,
		ParentSession:    "",
		Inputs:           map[string]any{},
		// Every shipped claude/codex task hook composes {{terminal "..."}}
		// against whichever task in the plan declares [terminal] — this
		// kitchen-sink render has no real tmux task in scope, so a stub
		// binding stands in for it.
		Terminal: &TerminalBinding{
			Ops: &config.TerminalConfig{
				Attach:   "tmux attach -t test-session",
				Capture:  "tmux capture-pane -p -t test-session",
				SendText: `tmux send-keys -t test-session -- "$1"`,
				SendKeys: `tmux send-keys -t test-session "$1"`,
			},
			Outputs: map[string]any{"session_name": "test-session"},
		},
	}

	// Superset of every key a shipped task's setup/cleanup/health probe reads
	// off .Prev/.Self, plus every input a shipped task declares.
	kitchen := map[string]any{
		"session_id":     "11111111-1111-1111-1111-111111111111",
		"pid":            12345,
		"socket_path":    "/tmp/claude-channel/x.sock",
		"mcp_config":     "/tmp/plect-mcp.json",
		"hooks_settings": "/tmp/plect-hooks.json",
		"profile_path":   "/tmp/plect-x.config.toml",
		"queue_dir":      "/tmp/plect-codex-exec/test-session/queue",
		"state_dir":      "/tmp/plect-codex-exec/test-session",
		"sent":           "true",
		"session_name":   "test-session",
		"dir":            "/tmp/plect-gh-guard.x",
	}
	inputs := map[string]any{
		"tmux_session":   "test-session",
		"terminal_ready": "test-session",
		"model":          "fable",
		"effort":         "high",
		"path_prepend":   "/tmp/plect-gh-guard.x",
		"template":       "",
		"agent_session":  "",
		"repeat":         "",
		"owner":          "acme",
	}

	for id, def := range tasks {
		ctx := RenderContext{
			Self:       kitchen,
			Prev:       kitchen,
			Inputs:     inputs,
			Session:    session,
			SourcePath: def.SourcePath,
		}
		if def.Setup != "" {
			if _, err := render(def.Setup, ctx); err != nil {
				t.Errorf("task %q setup: render: %v", id, err)
			}
		}
		if def.Cleanup != "" {
			if _, err := renderCleanup(def.Cleanup, ctx); err != nil {
				t.Errorf("task %q cleanup: render: %v", id, err)
			}
		}
		if alive := def.Health.AliveProbe(); alive != "" {
			if _, err := render(alive, ctx); err != nil {
				t.Errorf("task %q health.alive: render: %v", id, err)
			}
		}
		if activity := def.Health.ActivityProbe(); activity != "" {
			if _, err := render(activity, ctx); err != nil {
				t.Errorf("task %q health.activity: render: %v", id, err)
			}
		}
	}
}

// TestShippedCatalog_ModelEffortInputsRejectShellMetacharacters is a
// regression test: every shipped task's model/effort inputs are spliced
// directly into that task's own setup script (and, for claude/codex, resent
// as pane keystrokes), so a value carrying a shell metacharacter must be
// rejected by inputs_schema before it ever reaches that splice point. This
// previously held for agent/codex's tasks but not agent/claude's, letting a
// value like `x' ; rm -rf ~ ; echo '` break out of claude.toml's `MODEL='...'`
// literal at render time.
func TestShippedCatalog_ModelEffortInputsRejectShellMetacharacters(t *testing.T) {
	tasks, _ := loadShippedCatalogTasks(t)

	malicious := map[string]any{"tmux_session": "s", "model": "x' ; touch /tmp/pwned ; echo '"}
	benign := map[string]any{"tmux_session": "s", "model": "fable"}

	checked := 0
	for id, def := range tasks {
		props, ok := def.InputsSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := props["model"]; !ok {
			continue
		}
		checked++
		schema, err := CompileSchema(def.InputsSchema, def.ResolvedInputsSchemaPath(), "test:"+id)
		if err != nil {
			t.Fatalf("task %q: CompileSchema: %v", id, err)
		}
		if schema == nil {
			t.Fatalf("task %q declares a model input but compiled to no schema", id)
		}
		if err := schema.Validate(toJSONShape(malicious)); err == nil {
			t.Errorf("task %q: inputs_schema accepted a model value containing shell metacharacters", id)
		}
		if err := schema.Validate(toJSONShape(benign)); err != nil {
			t.Errorf("task %q: inputs_schema rejected a plain model token: %v", id, err)
		}
	}
	if checked == 0 {
		t.Fatal("no shipped task declares a model input; this test is checking nothing")
	}
}

// TestShippedCatalog_LaunchEnvRejectsQuoteBreakout: launch_env crosses the
// same splice point model/effort do — the setup script assigns it inside a
// single-quoted literal — so a value carrying a single quote must be rejected
// before the script ever renders, no matter how carefully the jq expansion
// downstream quotes what it parses.
func TestShippedCatalog_LaunchEnvRejectsQuoteBreakout(t *testing.T) {
	tasks, _ := loadShippedCatalogTasks(t)

	breakout := map[string]any{"tmux_session": "s", "launch_env": `{"A":"x'; touch /tmp/pwned; echo '"}`}
	benign := map[string]any{"tmux_session": "s", "launch_env": `{"PLECT_TEAM_CONTEXT":"acme"}`}

	checked := 0
	for id, def := range tasks {
		props, ok := def.InputsSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := props["launch_env"]; !ok {
			continue
		}
		checked++
		schema, err := CompileSchema(def.InputsSchema, def.ResolvedInputsSchemaPath(), "test:"+id)
		if err != nil {
			t.Fatalf("task %q: CompileSchema: %v", id, err)
		}
		if err := schema.Validate(toJSONShape(breakout)); err == nil {
			t.Errorf("task %q: inputs_schema accepted a launch_env value containing a single quote", id)
		}
		if err := schema.Validate(toJSONShape(benign)); err != nil {
			t.Errorf("task %q: inputs_schema rejected a plain launch_env object: %v", id, err)
		}
	}
	if checked == 0 {
		t.Fatal("no shipped task declares a launch_env input; this test is checking nothing")
	}
}
