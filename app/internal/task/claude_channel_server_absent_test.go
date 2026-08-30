//go:build integration

package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// pathWithoutExecutable returns a PATH value that resolves every command the
// ambient PATH resolves, except blocked. A host can have blocked installed
// somewhere on its own ambient PATH — e.g. this repo's own plugins built and
// registered system-wide — in a directory that also carries tools this
// test's script genuinely needs (jq, sleep, and the rest of coreutils);
// dropping that whole directory would break the run, but leaving it in would
// make a PATH-based presence check pass no matter what this test does to the
// plugin's own mounted directory, silently defeating the regression this
// test exists to catch. Flattening every PATH entry into one directory of
// symlinks, skipping only blocked, keeps everything else reachable.
func pathWithoutExecutable(t *testing.T, blocked string) string {
	t.Helper()
	mirror := t.TempDir()
	seen := map[string]bool{}
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == blocked || seen[name] {
				continue
			}
			target := filepath.Join(dir, name)
			if info, err := os.Stat(target); err != nil || info.IsDir() {
				continue
			}
			if err := os.Symlink(target, filepath.Join(mirror, name)); err != nil {
				continue
			}
			seen[name] = true
		}
	}
	return mirror
}

// A deployment can mount the claude plugin's manifest (declaring
// channel-server as an executable) without ever having built the binary —
// a custom catalog that omits it, or a builder stage that failed partway.
// channel_server_bin resolves to that path unconditionally (see
// app/internal/plugins/bin.go), so the graceful-skip behavior the runtime
// task's setup script promises has to come from a file-existence check on
// the resolved path, not from the binding itself. This is the one branch
// TestShippedEffects_InvocationsMatchTheirPluginsRecord's committed record
// cannot exercise: that harness's spyPlugin always writes an executable spy
// for every declared executable, so channel-server is never absent there.
func TestClaudeRuntimeSetup_SkipsChannelServerWhenBinaryAbsent(t *testing.T) {
	var source string
	for _, dir := range repoPluginDirs(t) {
		if filepath.Base(dir) == "claude" {
			source = dir
		}
	}
	if source == "" {
		t.Skip("plugins/claude not present in this tree")
	}

	h := newEffectHarness(t, source)

	var channelServerPath string
	for _, exec := range h.mounted.Manifest.Executables {
		if exec.Name == "channel-server" {
			channelServerPath = filepath.Join(h.mounted.Dir, exec.Path)
		}
	}
	if channelServerPath == "" {
		t.Fatal("plugins/claude does not declare a channel-server executable")
	}
	// This is the condition under test: the manifest declares channel-server,
	// so channel_server_bin still resolves to this path, but nothing was ever
	// built there.
	if err := os.Remove(channelServerPath); err != nil {
		t.Fatalf("remove channel-server spy: %v", err)
	}
	t.Setenv("PATH", h.spyDir+string(os.PathListSeparator)+pathWithoutExecutable(t, "channel-server"))

	cfg := &config.Config{PluginDirs: []string{h.mounted.Dir}, Plugins: []plugins.Mounted{h.mounted}}
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	var def config.TaskDefinition
	found := false
	for _, d := range defs {
		if d.ID == "runtime" {
			def = d
			found = true
		}
	}
	if !found {
		t.Fatal("plugins/claude does not declare a runtime task")
	}

	scenario := effectScenario{
		Hooks: []string{"setup"},
		Files: []effectScenarioFile{{
			Path:    ".claude/sessions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.json",
			Content: `{"sessionId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","pid":PID}`,
		}},
	}
	var b strings.Builder
	h.runScenario(t, &b, def, "runtime", scenario)
	out := b.String()

	if strings.Contains(out, "dangerously-load-development-channels") {
		t.Errorf("setup registered channel-server as an MCP channel despite the binary being absent:\n%s", out)
	}

	mcpConfigPath := filepath.Join(h.stateDir, "plect-mcp.json")
	raw, err := os.ReadFile(mcpConfigPath)
	if err != nil {
		t.Fatalf("read mcp_config: %v", err)
	}
	var mcpConfig struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &mcpConfig); err != nil {
		t.Fatalf("mcp_config is not valid JSON: %v\n%s", err, raw)
	}
	if len(mcpConfig.MCPServers) != 0 {
		t.Errorf("setup's mcp_config carries a registration despite the binary being absent: %s", raw)
	}
}
