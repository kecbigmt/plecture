package commands

import (
	"bytes"
	"fmt"
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

// The checkout carries its root's committed marker but is not trusted, so a
// search from the cwd selects the checkout rather than the session's root.
func sessionWithACheckout(t *testing.T) (session, configPath, root, checkout string) {
	t.Helper()
	configPath, _ = projectInvocationFixture(t)
	configHome := filepath.Dir(configPath)
	writeTestFile(t, filepath.Join(configHome, "workflows", "review.toml"), "[review]\nkind = \"workflow\"\nresource = \"issue\"\n[[review.nodes]]\nuses = \"noop\"\n")
	writeTestFile(t, filepath.Join(configHome, "tasks", "noop.toml"), "[noop]\nkind = \"effect\"\nscope = \"session\"\n[noop.setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n[noop.health.alive]\ntype = \"noop\"\n")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "repo")
	checkout = filepath.Join(base, "checkouts", "issue-1")
	for _, dir := range []string{root, checkout} {
		writeTestFile(t, filepath.Join(dir, ".plect", "project.toml"), "schema_version = 2\n")
		writeTestFile(t, filepath.Join(dir, ".plect", "workflows", "local.toml"), "[local]\nkind = \"workflow\"\nresource = \"issue\"\n[[local.nodes]]\nuses = \"noop\"\n")
	}
	writeTestFile(t, configPath, invocationGlobalConfig+"trusted_project_roots = [\""+root+"\"]\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(cfg, state.NewStore(""), service.CreateParams{URL: "https://forge.example/issues/1", Workflow: "local", Tag: "work", ProjectRoot: root})
	if err != nil {
		t.Fatalf("Create in the trusted root: %v", err)
	}
	t.Chdir(checkout)
	t.Setenv("PLECT_SESSION_NAME", created.SessionName)
	return created.SessionName, configPath, root, checkout
}

func nonInteractive(t *testing.T) {
	t.Helper()
	origInteractive := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = origInteractive })
}

func TestUp_InACheckoutWithTheSessionVariableCreatesTheChildInTheSessionsRoot(t *testing.T) {
	parent, configPath, root, _ := sessionWithACheckout(t)
	before, _ := os.ReadFile(configPath)
	nonInteractive(t)

	out, err := execRoot(t, "up", "https://forge.example/issues/2", "--workflow", "local", "--tag", "child", "--detach")
	if err != nil {
		t.Fatalf("up from the checkout: %v; output:\n%s", err, out)
	}
	child := state.NewStore("").Get("issue-2+child")
	if child == nil {
		t.Fatalf("no child session; output:\n%s", out)
	}
	if child.ProjectRoot != root || child.ParentSession != parent {
		t.Errorf("child project_root = %q, parent = %q; want %q, %q", child.ProjectRoot, child.ParentSession, root, parent)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestUp_InACheckoutStopsAtTheSessionsRevokedRootInsteadOfFallingBack(t *testing.T) {
	_, configPath, root, checkout := sessionWithACheckout(t)
	// A same-id global workflow is what a fallback to the global-only
	// context would silently run.
	writeTestFile(t, filepath.Join(filepath.Dir(configPath), "workflows", "local.toml"), "[local]\nkind = \"workflow\"\nresource = \"issue\"\n[[local.nodes]]\nuses = \"noop\"\n")
	writeTestFile(t, configPath, invocationGlobalConfig)
	nonInteractive(t)

	out, err := execRoot(t, "up", "https://forge.example/issues/2", "--workflow", "local", "--tag", "child", "--detach")
	if err == nil {
		t.Fatalf("up succeeded after the session's root lost its trust; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), root) || strings.Contains(err.Error(), checkout) {
		t.Errorf("error %q must name the session's root %s, not the checkout", err, root)
	}
	if child := state.NewStore("").Get("issue-2+child"); child != nil {
		t.Errorf("child session created in context %q", child.ProjectRoot)
	}
}

func TestWorkflowList_InACheckoutWithTheSessionVariableDoesNotAskToTrustTheCheckout(t *testing.T) {
	_, configPath, _, checkout := sessionWithACheckout(t)
	before, _ := os.ReadFile(configPath)
	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = origInteractive })
	workflowListJSON, workflowListNoHeader = false, true
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetIn(strings.NewReader("y\n"))
	rootCmd.SetArgs([]string{"workflow", "list", "--no-header"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})

	if err := rootCmd.Execute(); err != nil || !strings.Contains(out.String(), "local") {
		t.Fatalf("workflow list = %v, output %q; want the session root's workflow", err, out.String())
	}
	if strings.Contains(errOut.String(), checkout) {
		t.Errorf("asked about the checkout:\n%s", errOut.String())
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestWorkflowList_InACheckoutOfAGlobalOnlySessionUsesTheGlobalOnlyContext(t *testing.T) {
	_, configPath, _, _ := sessionWithACheckout(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(cfg, state.NewStore(""), service.CreateParams{URL: "https://forge.example/issues/3", Workflow: "review", Tag: "work"})
	if err != nil {
		t.Fatalf("Create in the global-only context: %v", err)
	}
	t.Setenv("PLECT_SESSION_NAME", created.SessionName)
	before, _ := os.ReadFile(configPath)

	out, err := runWorkflowList(t, true, "y\n")
	if err != nil || !strings.Contains(out, "review") || strings.Contains(out, "local") {
		t.Fatalf("workflow list = %v, output %q; want only the global workflows", err, out)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestWorkflowList_ASessionVariableNamingNoSessionSelectsFromTheCwd(t *testing.T) {
	_, _, _, checkout := sessionWithACheckout(t)
	t.Setenv("PLECT_SESSION_NAME", "no-such-session")

	_, err := runWorkflowList(t, false, "")
	if err == nil || !strings.Contains(err.Error(), checkout) {
		t.Fatalf("workflow list = %v; want the cwd's untrusted root %s", err, checkout)
	}
}

func checkoutEffect(id, dir string) string {
	return fmt.Sprintf(`[%[1]s]
kind  = "effect"
scope = "session"

[%[1]s.outputs_schema]
type = "object"

[%[1]s.outputs_schema.properties]
workspace_dir = { type = "string" }

[%[1]s.setup]
type   = "shell"
script = '''
mkdir -p %[2]q
printf '{"workspace_dir":"%[2]s"}'
'''

[%[1]s.health.alive]
type = "noop"
`, id, dir)
}

func workdirWorkflow(id, uses string) string {
	return fmt.Sprintf("[%[1]s]\nkind = \"workflow\"\nresource = \"issue\"\nworkdir = { from = \"nodes.%[2]s.outputs.workspace_dir\" }\n[[%[1]s.nodes]]\nuses = %[2]q\n", id, uses)
}

func sessionOwningACheckout(t *testing.T) (session, configPath, root, checkout string) {
	t.Helper()
	t.Setenv("PLECT_SESSION_NAME", "")
	configPath, _ = projectInvocationFixture(t)
	configHome := filepath.Dir(configPath)
	t.Setenv("HOME", t.TempDir())
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "repo")
	checkout = filepath.Join(base, "checkouts", "issue-1")
	writeTestFile(t, filepath.Join(configHome, "tasks", "checkout.toml"), checkoutEffect("checkout", checkout))
	for _, dir := range []string{root, checkout} {
		writeTestFile(t, filepath.Join(dir, ".plect", "project.toml"), "schema_version = 2\n")
		writeTestFile(t, filepath.Join(dir, ".plect", "workflows", "local.toml"), workdirWorkflow("local", "checkout"))
	}
	writeTestFile(t, configPath, invocationGlobalConfig+"trusted_project_roots = [\""+root+"\"]\n")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Up(cfg, state.NewStore(""), service.UpParams{Identifier: "https://forge.example/issues/1", Workflow: "local", Tag: "work", ProjectRoot: root})
	if err != nil {
		t.Fatalf("Up in the trusted root: %v", err)
	}
	below := filepath.Join(checkout, "src")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(below)
	return created.SessionName, configPath, root, checkout
}

func TestUp_InASessionsCheckoutWithoutTheSessionVariableCreatesTheChildInTheSessionsRoot(t *testing.T) {
	_, configPath, root, _ := sessionOwningACheckout(t)
	before, _ := os.ReadFile(configPath)
	nonInteractive(t)

	out, err := execRoot(t, "up", "https://forge.example/issues/2", "--workflow", "local", "--tag", "child", "--detach")
	if err != nil {
		t.Fatalf("up from the checkout: %v; output:\n%s", err, out)
	}
	if child := state.NewStore("").Get("issue-2+child"); child == nil || child.ProjectRoot != root {
		t.Fatalf("child = %+v; want a session in %s", child, root)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestUp_InASessionsCheckoutStopsAtTheSessionsRevokedRoot(t *testing.T) {
	_, configPath, root, checkout := sessionOwningACheckout(t)
	writeTestFile(t, configPath, invocationGlobalConfig)
	nonInteractive(t)

	out, err := execRoot(t, "up", "https://forge.example/issues/2", "--workflow", "local", "--tag", "child", "--detach")
	if err == nil {
		t.Fatalf("up succeeded after the session's root lost its trust; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), root) || strings.Contains(err.Error(), checkout) {
		t.Errorf("error %q must name the session's root %s, not the checkout", err, root)
	}
	if child := state.NewStore("").Get("issue-2+child"); child != nil {
		t.Errorf("child session created in context %q", child.ProjectRoot)
	}
}

func TestWorkflowList_InASessionsCheckoutDoesNotAskToTrustTheCheckout(t *testing.T) {
	_, configPath, _, checkout := sessionOwningACheckout(t)
	before, _ := os.ReadFile(configPath)

	out, err := runWorkflowList(t, true, "y\n")
	if err != nil || !strings.Contains(out, "local") {
		t.Fatalf("workflow list = %v, output %q; want the session root's workflow", err, out)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(before) || strings.Contains(string(after), checkout) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestWorkflowList_InNestedCheckoutsTheDeepestWorkdirsSessionDecides(t *testing.T) {
	_, configPath, _, checkout := sessionOwningACheckout(t)
	configHome := filepath.Dir(configPath)
	writeTestFile(t, filepath.Join(configHome, "tasks", "outer_checkout.toml"), checkoutEffect("outer_checkout", filepath.Dir(checkout)))
	writeTestFile(t, filepath.Join(configHome, "workflows", "outer.toml"), workdirWorkflow("outer", "outer_checkout"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Up(cfg, state.NewStore(""), service.UpParams{Identifier: "https://forge.example/issues/3", Workflow: "outer", Tag: "work"}); err != nil {
		t.Fatalf("Up the enclosing global-only session: %v", err)
	}

	out, err := runWorkflowList(t, false, "")
	if err != nil || !strings.Contains(out, "local") {
		t.Fatalf("workflow list = %v, output %q; want the inner session's root", err, out)
	}
}

func TestWorkflowList_SessionsSharingTheDeepestWorkdirWithDifferentRootsStop(t *testing.T) {
	session, configPath, _, _ := sessionOwningACheckout(t)
	writeTestFile(t, filepath.Join(filepath.Dir(configPath), "workflows", "shared.toml"), workdirWorkflow("shared", "checkout"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.Up(cfg, state.NewStore(""), service.UpParams{Identifier: "https://forge.example/issues/3", Workflow: "shared", Tag: "work"})
	if err != nil {
		t.Fatalf("Up the global-only session sharing the checkout: %v", err)
	}

	out, err := runWorkflowList(t, true, "y\n")
	if err == nil {
		t.Fatalf("workflow list succeeded; output %q", out)
	}
	if !strings.Contains(err.Error(), session) || !strings.Contains(err.Error(), other.SessionName) {
		t.Errorf("error %q must name both sessions", err)
	}
}

func TestWorkflowList_TheSessionVariableNeedsNoReadableCwd(t *testing.T) {
	_, _, _, checkout := sessionWithACheckout(t)
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}

	out, err := runWorkflowList(t, false, "")
	if err != nil || !strings.Contains(out, "local") {
		t.Fatalf("workflow list = %v, output %q; want the session root's workflow", err, out)
	}
}
