package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/state"
)

const projectTicketURL = "https://forge.example/tickets/7"

func projectResourceFixture(t *testing.T) (root, outside string) {
	t.Helper()
	t.Setenv("PLECT_SESSION_NAME", "")
	configPath, root := projectInvocationFixture(t)
	writeTestFile(t, configPath, invocationGlobalConfig+"trusted_project_roots = [\""+root+"\"]\n")
	writeTestFile(t, filepath.Join(root, ".plect", "resources", "ticket.toml"), `
[ticket]
kind  = "resource"
match = '^https://forge\.example/tickets/(?P<number>\d+)$'
name  = { expr = "'ticket-' + match.number" }

[ticket.observe]
type    = "exec"
command = "sh"
args    = ["-c", 'printf "{\"status\":\"open\"}"']
`)
	writeTestFile(t, filepath.Join(root, ".plect", "workflows", "ticketed.toml"), "[ticketed]\nkind = \"workflow\"\nresource = \"ticket\"\n[[ticketed.nodes]]\nuses = \"noop\"\n")
	writeTestFile(t, filepath.Join(root, ".plect", "tasks", "noop.toml"), "[noop]\nkind = \"effect\"\nscope = \"session\"\n[noop.setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n[noop.health.alive]\ntype = \"noop\"\n")
	outside = filepath.Join(filepath.Dir(root), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	nonInteractive(t)
	return root, outside
}

func TestResourceStatus_ResolvesAResourceTheTrustedProjectRootDeclares(t *testing.T) {
	_, outside := projectResourceFixture(t)

	out, err := execRoot(t, "resource", "status", projectTicketURL)
	if err != nil {
		t.Fatalf("resource status below the root: %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "ticket") || !strings.Contains(out, "open") {
		t.Errorf("output %q, want the project resource's observation", out)
	}

	t.Chdir(outside)
	if out, err := execRoot(t, "resource", "status", projectTicketURL); err == nil {
		t.Errorf("resource status outside the root recognized the project's resource; output:\n%s", out)
	}
}

func TestUp_CreatesASessionForAResourceTheTrustedProjectRootDeclares(t *testing.T) {
	root, outside := projectResourceFixture(t)

	t.Chdir(outside)
	if out, err := execRoot(t, "up", projectTicketURL, "--workflow", "ticketed", "--tag", "work", "--detach"); err == nil {
		t.Fatalf("up outside the root created a session for the project's resource; output:\n%s", out)
	}
	if s := state.NewStore("").Get("ticket-7+work"); s != nil {
		t.Fatalf("session created outside the root in context %q", s.ProjectRoot)
	}

	t.Chdir(filepath.Join(root, "src"))
	out, err := execRoot(t, "up", projectTicketURL, "--workflow", "ticketed", "--tag", "work", "--detach")
	if err != nil {
		t.Fatalf("up below the root: %v; output:\n%s", err, out)
	}
	session := state.NewStore("").Get("ticket-7+work")
	if session == nil || session.ProjectRoot != root {
		t.Fatalf("session = %+v, want ticket-7+work in context %q", session, root)
	}
}
