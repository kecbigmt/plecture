package task

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// claudeMCPBase is what the claude task has already assembled by the time it
// merges the mcp_servers parameter: its own channel-server registration.
const claudeMCPBase = `{"mcpServers":{"channel-server":{"command":"channel-server","env":{"CHANNEL_SOCKET_PATH":"/tmp/x.sock"}}}}`

func runClaudeMCPServers(t *testing.T, records string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	script := filepath.Join(repoRootForTest(t), "plugins", "claude", "scripts", "claude-mcp-servers")
	cmd := exec.Command(script)
	cmd.Stdin = strings.NewReader(claudeMCPBase)
	cmd.Env = append(os.Environ(), "CLAUDE_MCP_SERVERS="+records)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// The mcp_servers parameter is serialized into the agent's own MCP config and
// nowhere else, so the merge is the whole observable contract: the task's own
// registrations survive, and each accepted record turns into one entry.
func TestShippedClaude_McpServersMerge(t *testing.T) {
	cases := []struct {
		name    string
		records string
		want    map[string]any
	}{
		{
			name:    "no parameter leaves the task's own registrations untouched",
			records: "",
			want: map[string]any{
				"channel-server": map[string]any{"command": "channel-server", "env": map[string]any{"CHANNEL_SOCKET_PATH": "/tmp/x.sock"}},
			},
		},
		{
			name:    "empty array leaves the task's own registrations untouched",
			records: `[]`,
			want: map[string]any{
				"channel-server": map[string]any{"command": "channel-server", "env": map[string]any{"CHANNEL_SOCKET_PATH": "/tmp/x.sock"}},
			},
		},
		{
			name:    "records land alongside the task's own defaults",
			records: `[{"name":"kbn","command":"kbn-mcp","args":["--scoped"]},{"name":"docs","command":"docs-mcp","env":{"DOCS_ROOT":"/srv/docs"}}]`,
			want: map[string]any{
				"channel-server": map[string]any{"command": "channel-server", "env": map[string]any{"CHANNEL_SOCKET_PATH": "/tmp/x.sock"}},
				"kbn":            map[string]any{"command": "kbn-mcp", "args": []any{"--scoped"}},
				"docs":           map[string]any{"command": "docs-mcp", "env": map[string]any{"DOCS_ROOT": "/srv/docs"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runClaudeMCPServers(t, tc.records)
			if err != nil {
				t.Fatalf("claude-mcp-servers: %v (stderr: %s)", err, stderr)
			}
			var got struct {
				MCPServers map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("output is not JSON: %v (%s)", err, stdout)
			}
			wantJSON, _ := json.Marshal(tc.want)
			gotJSON, _ := json.Marshal(got.MCPServers)
			if string(wantJSON) != string(gotJSON) {
				t.Errorf("mcpServers = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// A malformed record must fail the launch naming both the offending record and
// the field, because the alternative — a silently dropped or half-formed
// registration — surfaces much later as a missing tool inside the agent.
func TestShippedClaude_McpServersRejectsMalformedRecord(t *testing.T) {
	cases := []struct {
		name    string
		records string
		want    []string
	}{
		{"not an array", `{"name":"kbn","command":"kbn-mcp"}`, []string{"mcp_servers", "array"}},
		{"not JSON at all", `kbn-mcp`, []string{"mcp_servers"}},
		{"record is not an object", `["kbn-mcp"]`, []string{"index 0", "object"}},
		{"missing name", `[{"command":"kbn-mcp"}]`, []string{"index 0", "name"}},
		{"empty name", `[{"name":"","command":"kbn-mcp"}]`, []string{"index 0", "name"}},
		{"missing command", `[{"name":"kbn"}]`, []string{"kbn", "command"}},
		{"command is not a string", `[{"name":"kbn","command":["kbn-mcp"]}]`, []string{"kbn", "command"}},
		{"args is not an array", `[{"name":"kbn","command":"kbn-mcp","args":"--scoped"}]`, []string{"kbn", "args"}},
		{"args holds a non-string", `[{"name":"kbn","command":"kbn-mcp","args":[1]}]`, []string{"kbn", "args"}},
		{"env is not an object of strings", `[{"name":"kbn","command":"kbn-mcp","env":{"A":1}}]`, []string{"kbn", "env"}},
		{"unknown field", `[{"name":"kbn","command":"kbn-mcp","arg":["--scoped"]}]`, []string{"kbn", "arg"}},
		{"collides with the task's own registration", `[{"name":"channel-server","command":"impostor"}]`, []string{"channel-server"}},
		{"two records claim one name", `[{"name":"kbn","command":"a"},{"name":"kbn","command":"b"}]`, []string{"kbn"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runClaudeMCPServers(t, tc.records)
			if err == nil {
				t.Fatalf("want a non-zero exit, got success (stderr: %s)", stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q does not name %q", stderr, want)
				}
			}
		})
	}
}

// The parameter is data: it may reach the agent's config file and nothing
// else. Under the binding transport that is structural — the value reaches
// the script through its private binding file, so the shell source the
// process runs cannot contain it, and neither can the argv.
func TestShippedClaude_McpServersStaysOffEveryCommandLine(t *testing.T) {
	tasks, mounted := loadShippedCatalogTasks(t)
	def, ok := tasks["runtime"]
	if !ok {
		t.Fatal("shipped catalog has no claude task")
	}

	const marker = "MCPSERVERSMARKER"
	ctx := RenderContext{
		Self: map[string]any{},
		Prev: map[string]any{},
		Inputs: map[string]any{
			"tmux_session": "s",
			"mcp_servers":  `[{"name":"` + marker + `","command":"` + marker + `-mcp"}]`,
		},
		Session: SessionVars{
			Name:             "test-session",
			WorkspaceDirPath: t.TempDir(),
			Plugins:          mounted,
			Terminal: &TerminalBinding{
				Ops:     &config.TerminalConfig{Attach: shellStub("a"), Capture: shellStub("c"), SendText: shellStub("t"), SendKeys: shellStub("k")},
				Outputs: map[string]any{"session_name": "test-session"},
			},
		},
		SourcePath: def.SourcePath,
	}
	resolved, err := resolveEffect(def.Setup, setupRoots(ctx), ctx, def.Ownership(), nil)
	if err != nil {
		t.Fatalf("resolve setup: %v", err)
	}
	defer resolved.close()
	for _, arg := range resolved.execution.Argv {
		if strings.Contains(arg, marker) {
			t.Errorf("mcp_servers value reaches the process argv: %q", arg)
		}
	}
	script, err := os.ReadFile(filepath.Join(filepath.Dir(resolved.execution.Argv[0]), "script.sh"))
	if err != nil {
		t.Fatalf("read the resolved script: %v", err)
	}
	if strings.Contains(string(script), marker) {
		t.Error("mcp_servers value reaches the shell source the process runs")
	}
	bindings, err := os.ReadFile(filepath.Join(filepath.Dir(resolved.execution.Argv[0]), "bindings.sh"))
	if err != nil {
		t.Fatalf("read the resolved bindings: %v", err)
	}
	if !strings.Contains(string(bindings), marker) {
		t.Error("mcp_servers value never reached the binding file, so this test is checking nothing")
	}
}

// Parameterization composes with nesting: an outer effect must be able to
// bind mcp_servers into the shipped claude effect through `inner.inputs`,
// which the inner effect's closed inputs schema only permits once the
// parameter is declared.
func TestShippedClaude_McpServersBindsThroughNesting(t *testing.T) {
	repoRoot := repoRootForTest(t)
	claude := mountShippedPlugin(t, repoRoot, "official", "plugins/claude")
	base := t.TempDir()
	outer := filepath.Join(base, "tasks", "team_claude.toml")
	if err := os.MkdirAll(filepath.Dir(outer), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `
[team_claude]
kind = "effect"

[team_claude.inner]
uses = "official/claude/runtime"

[team_claude.inner.inputs]
tmux_session = { from = "inputs.tmux_session" }
mcp_servers  = { from = "inputs.mcp_servers" }

[team_claude.inputs_schema]
type     = "object"
required = ["tmux_session"]

[team_claude.inputs_schema.properties]
tmux_session = { type = "string" }
mcp_servers  = { type = "string" }
`
	if err := os.WriteFile(outer, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		PluginDirs: []string{claude.Dir},
		Plugins:    []plugins.Mounted{claude},
		BaseDir:    base,
	}
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions with an mcp_servers binding: %v", err)
	}
	if len(defs["team_claude"].InnerChain) != 1 {
		t.Fatalf("team_claude did not resolve its inner claude layer")
	}
}

// The records must stay out of every process argv, not just out of the setup
// script's own command lines: argv is world-readable on a shared host, so a
// record carrying a credential in `env` would leak to any local process
// watching /proc. A stub jq that logs its own argv is what makes the
// serialization step's exec boundary observable — the merge itself is covered
// above, so this case cares only about what crosses argv.
func TestShippedClaude_McpServersStaysOutOfProcessArgv(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	const marker = "MCPSERVERSMARKER"
	binDir := t.TempDir()
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	stub := "#!/usr/bin/env bash\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"" + argvLog + "\"; done\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "jq"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(repoRootForTest(t), "plugins", "claude", "scripts", "claude-mcp-servers")
	cmd := exec.Command(script)
	cmd.Stdin = strings.NewReader(claudeMCPBase)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		`CLAUDE_MCP_SERVERS=[{"name":"`+marker+`","command":"`+marker+`-mcp","env":{"TOKEN":"`+marker+`-secret"}}]`,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("claude-mcp-servers: %v (stderr: %s)", err, stderr.String())
	}

	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("the stub jq was never executed, so this test is checking nothing: %v", err)
	}
	for _, arg := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		if strings.Contains(arg, marker) {
			t.Errorf("an mcp_servers record reaches a child process argv: %q", arg)
		}
	}
}
