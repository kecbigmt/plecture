package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// parseActionSource decodes one action's TOML into the parsed form Eval
// consumes, so a test states the action the way an author writes it.
func parseActionSource(t *testing.T, src string) *Action {
	t.Helper()
	var raw map[string]any
	if _, err := toml.Decode(src, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	action, err := ParseAction(raw, Position{File: "test.toml", Path: "action"})
	if err != nil {
		t.Fatalf("parse action: %v", err)
	}
	return action
}

func observerEval(env Environment) Eval {
	return Eval{
		Env: env,
		Bin: func(ref string) (string, error) { return "/plugins/bin/" + ref, nil },
	}
}

func TestEvalExecResolvesEveryValueForm(t *testing.T) {
	action := parseActionSource(t, `
type = "exec"
bin  = "github-issue-pr"
args = [
  "observe",
  "--resource",
  { from = "resource.id" },
  "--workspace-dir-path",
  { from = "workspace.dir", default = "" },
  "--watcher-bin",
  { bin = "github-watcher" },
  "--label",
  { expr = "'pr-' + resource.id" },
]
`)
	got, err := observerEval(Environment{
		"resource": map[string]any{"id": "https://example.test/pull/1"},
	}).Exec(action)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/plugins/bin/github-issue-pr",
		"observe",
		"--resource", "https://example.test/pull/1",
		"--workspace-dir-path", "",
		"--watcher-bin", "/plugins/bin/github-watcher",
		"--label", "pr-https://example.test/pull/1",
	}
	if strings.Join(got.Argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("Argv =\n%q\nwant\n%q", got.Argv, want)
	}
	if len(got.Stdin) != 0 {
		t.Errorf("Stdin = %q, want empty", got.Stdin)
	}
}

func TestEvalExecCommandNamesTheExecutableDirectly(t *testing.T) {
	action := parseActionSource(t, `
type    = "exec"
command = "git"
args    = ["rev-parse", { from = "workspace.branch" }]
`)
	got, err := observerEval(Environment{
		"workspace": map[string]any{"branch": "main"},
	}).Exec(action)
	if err != nil {
		t.Fatal(err)
	}
	if got.Argv[0] != "git" || got.Argv[2] != "main" {
		t.Errorf("Argv = %q", got.Argv)
	}
}

func TestEvalExecStdinSerializesAJSONOperand(t *testing.T) {
	action := parseActionSource(t, `
type  = "exec"
bin   = "okf-goal"
args  = ["resource", "finalize"]
stdin = { json = { from = "judges" } }
`)
	got, err := observerEval(Environment{
		"judges": []any{map[string]any{"id": "ac-met", "reason": "it's fine"}},
	}).Exec(action)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"id":"ac-met","reason":"it's fine"}]`; string(got.Stdin) != want {
		t.Errorf("Stdin = %s, want %s", got.Stdin, want)
	}
}

func TestEvalAbsentProjectionIsAnErrorWithoutDefaultOrOptional(t *testing.T) {
	action := parseActionSource(t, `
type = "exec"
bin  = "github-issue-pr"
args = [{ from = "workspace.dir" }]
`)
	_, err := observerEval(Environment{}).Exec(action)
	if err == nil || !strings.Contains(err.Error(), "workspace.dir") {
		t.Fatalf("expected an absent-root error naming workspace.dir, got %v", err)
	}
}

func TestEvalOptionalProjectionOmitsTheArgvElement(t *testing.T) {
	action := parseActionSource(t, `
type = "exec"
bin  = "github-issue-pr"
args = ["observe", { from = "workspace.dir", optional = true }]
`)
	got, err := observerEval(Environment{}).Exec(action)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Argv) != 2 || got.Argv[1] != "observe" {
		t.Errorf("Argv = %q, want the absent element omitted", got.Argv)
	}
}

func TestEvalStringifiesNativeScalarTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want string
	}{
		{"string", "plain", "plain"},
		{"bool", true, "true"},
		{"int", int64(7), "7"},
		{"float", 1.5, "1.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, absent, err := observerEval(Environment{"inputs": map[string]any{"v": tc.val}}).
				Argument(&Value{Form: FormFrom, From: "inputs.v"})
			if err != nil || absent {
				t.Fatalf("absent=%v err=%v", absent, err)
			}
			if got != tc.want {
				t.Errorf("Argument = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvalArgumentRejectsAComposite(t *testing.T) {
	_, _, err := observerEval(Environment{"inputs": map[string]any{"v": []any{"a", "b"}}}).
		Argument(&Value{Form: FormFrom, From: "inputs.v"})
	if err == nil || !strings.Contains(err.Error(), "json") {
		t.Fatalf("expected an error pointing at the json serializer, got %v", err)
	}
}

func TestEvalExprNamingAnAbsentRootFails(t *testing.T) {
	_, _, err := observerEval(Environment{}).
		Argument(&Value{Form: FormExpr, Expr: "resource.id"})
	if err == nil || !strings.Contains(err.Error(), "resource") {
		t.Fatalf("expected an unresolved-name error naming resource, got %v", err)
	}
}

func TestEvalShellMaterializesBindingsWithoutTouchingArgv(t *testing.T) {
	action := parseActionSource(t, `
type   = "shell"
script = "echo \"$resource_id\"\n"

[bind]
resource_id = { from = "resource.id" }
watcher     = { bin = "github-watcher" }
`)
	dir := t.TempDir()
	got, err := observerEval(Environment{
		"resource": map[string]any{"id": "https://example.test/pull/1"},
	}).Shell(filepath.Join(dir, "run"), action, nil)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := os.ReadFile(filepath.Join(dir, "run", "bindings.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := "resource_id='https://example.test/pull/1'\nwatcher='/plugins/bin/github-watcher'\n"
	if string(bindings) != want {
		t.Errorf("bindings =\n%q\nwant\n%q", bindings, want)
	}
	if len(got.Argv) != 1 || got.Argv[0] != filepath.Join(dir, "run", "run.sh") {
		t.Errorf("Argv = %q, want just the generated wrapper", got.Argv)
	}
}

func TestEvalShellRejectsAnAbsentBinding(t *testing.T) {
	action := parseActionSource(t, `
type   = "shell"
script = "echo hi\n"

[bind]
dir = { from = "workspace.dir", optional = true }
`)
	_, err := observerEval(Environment{}).Shell(t.TempDir(), action, nil)
	if err == nil || !strings.Contains(err.Error(), "dir") {
		t.Fatalf("expected an absent-binding error naming dir, got %v", err)
	}
}

func TestEvalRejectsTheOtherActionVariant(t *testing.T) {
	exec := parseActionSource(t, `
type = "exec"
bin  = "okf-goal"
`)
	if _, err := observerEval(nil).Shell(t.TempDir(), exec, nil); err == nil {
		t.Error("Shell accepted an exec action")
	}
	shell := parseActionSource(t, `
type   = "shell"
script = "echo hi\n"
`)
	if _, err := observerEval(nil).Exec(shell); err == nil {
		t.Error("Exec accepted a shell action")
	}
}

func TestEvalRunDispatchesOnTheActionVariant(t *testing.T) {
	exec := parseActionSource(t, `
type = "exec"
bin  = "okf-goal"
args = ["observe"]
`)
	got, err := observerEval(nil).Run("", exec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Argv[0] != "/plugins/bin/okf-goal" {
		t.Errorf("Argv = %q, want the resolved executable", got.Argv)
	}

	shell := parseActionSource(t, `
type   = "shell"
script = "echo hi\n"
`)
	dir := t.TempDir()
	got, err = observerEval(nil).Run(dir, shell, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Argv[0] != filepath.Join(dir, "run.sh") {
		t.Errorf("Argv = %q, want the generated wrapper", got.Argv)
	}
}

func TestEvalTerminalCapabilityIsUnavailableWhereNoResolverIsSupplied(t *testing.T) {
	_, _, err := observerEval(nil).Argument(&Value{Form: FormTerminal, Terminal: "send_text"})
	if err == nil || !strings.Contains(err.Error(), "send_text") {
		t.Fatalf("expected a terminal-unavailable error naming send_text, got %v", err)
	}
}
