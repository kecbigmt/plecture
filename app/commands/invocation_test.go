package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

const invocationGlobalConfig = "schema_version = 2\n"

// projectInvocationFixture lays out a machine config home and an untrusted
// project root declaring one workflow, and runs every command from below that
// root.
func projectInvocationFixture(t *testing.T) (configPath, root string) {
	t.Helper()
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	t.Setenv("PLECT_CONFIG_HOME", configHome)
	t.Setenv("PLECT_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	configPath = filepath.Join(configHome, "config.toml")
	writeTestFile(t, configPath, invocationGlobalConfig)
	writeTestFile(t, filepath.Join(configHome, "resources", "issue.toml"), `
[issue]
kind  = "resource"
match = '^https://forge\.example/issues/(?P<number>\d+)$'
name  = { expr = "'issue-' + match.number" }

[issue.observe]
type    = "exec"
command = "true"
`)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "widgets")
	writeTestFile(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
	writeTestFile(t, filepath.Join(root, ".plect", "workflows", "local.toml"), "[local]\nkind = \"workflow\"\nresource = \"issue\"\n")
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	return configPath, root
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runWorkflowList(t *testing.T, interactive bool, stdin string) (string, error) {
	t.Helper()
	origInteractive := isInteractive
	isInteractive = func() bool { return interactive }
	t.Cleanup(func() { isInteractive = origInteractive })
	workflowListJSON, workflowListNoHeader = false, true
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs([]string{"workflow", "list", "--no-header"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestUntrustedProjectRoot_NonInteractiveStopsWithTheRootToTrust(t *testing.T) {
	configPath, root := projectInvocationFixture(t)

	out, err := runWorkflowList(t, false, "")
	if err == nil {
		t.Fatalf("workflow list succeeded below an untrusted root; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), "trusted_project_roots") {
		t.Errorf("error %q must name the root and trusted_project_roots", err)
	}
	if body, _ := os.ReadFile(configPath); string(body) != invocationGlobalConfig {
		t.Errorf("config.toml changed without a trust decision:\n%s", body)
	}
}

func TestUntrustedProjectRoot_InteractiveAnswerDecidesTrust(t *testing.T) {
	for _, tc := range []struct {
		answer  string
		trusted bool
	}{
		{"y", true},
		{"n", false},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			configPath, root := projectInvocationFixture(t)

			out, err := runWorkflowList(t, true, tc.answer+"\n")
			body, _ := os.ReadFile(configPath)
			recorded := strings.Contains(string(body), root)
			if recorded != tc.trusted {
				t.Errorf("root recorded = %v, want %v; config.toml:\n%s", recorded, tc.trusted, body)
			}
			if tc.trusted {
				if err != nil || !strings.Contains(out, "local") {
					t.Fatalf("workflow list = %v, output %q; want the project's workflow in the same invocation", err, out)
				}
				return
			}
			if err == nil || strings.Contains(out, "local") {
				t.Errorf("workflow list = %v, output %q; want the invocation stopped", err, out)
			}
		})
	}
}

func TestTrustedProjectRoot_IsSelectedWithoutAPrompt(t *testing.T) {
	configPath, root := projectInvocationFixture(t)
	writeTestFile(t, configPath, invocationGlobalConfig+"trusted_project_roots = [\""+root+"\"]\n")

	out, err := runWorkflowList(t, false, "")
	if err != nil || !strings.Contains(out, "local") {
		t.Fatalf("workflow list = %v, output %q; want the trusted project's workflow", err, out)
	}
}

// sessionInTrustedRoot creates a session in trusted root A and returns its
// name, the config.toml path, and an untrusted marked root B to run from.
func sessionInTrustedRoot(t *testing.T) (session, configPath, other string) {
	t.Helper()
	configPath, untrusted := projectInvocationFixture(t)
	configHome := filepath.Dir(configPath)
	writeTestFile(t, filepath.Join(configHome, "workflows", "review.toml"), "[review]\nkind = \"workflow\"\nresource = \"issue\"\n[[review.nodes]]\nuses = \"noop\"\n")
	writeTestFile(t, filepath.Join(configHome, "tasks", "noop.toml"), "[noop]\nkind = \"effect\"\nscope = \"session\"\n[noop.setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n[noop.health.alive]\ntype = \"noop\"\n")
	trusted := filepath.Join(filepath.Dir(untrusted), "trusted")
	writeTestFile(t, filepath.Join(trusted, ".plect", "project.toml"), "schema_version = 2\n")
	writeTestFile(t, configPath, invocationGlobalConfig+"trusted_project_roots = [\""+trusted+"\"]\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(cfg, state.NewStore(""), service.CreateParams{URL: "https://forge.example/issues/1", Workflow: "review", Tag: "work", ProjectRoot: trusted})
	if err != nil {
		t.Fatalf("Create in the trusted root: %v", err)
	}
	return created.SessionName, configPath, untrusted
}

func TestUp_BySessionNameIgnoresTheCallersUntrustedRoot(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	session, configPath, _ := sessionInTrustedRoot(t)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	origInteractive := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = origInteractive })

	out, err := execRoot(t, "up", session, "--detach")
	if err != nil {
		t.Fatalf("up %s below an untrusted root: %v; output:\n%s", session, err, out)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}
