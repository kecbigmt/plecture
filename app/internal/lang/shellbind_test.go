package lang

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bindingScript = `printf '%s|%s|%s' "$session_name" "$send_text" "$1"
`

func materializeForTest(t *testing.T, script string, bound map[string]string, operands ...string) (*ShellExecution, string) {
	t.Helper()
	a, err := ParseAction(map[string]any{
		"type":   "shell",
		"script": script,
		"bind": map[string]any{
			"session_name": map[string]any{"from": "session.name"},
			"send_text":    map[string]any{"terminal": "send_text"},
		},
	}, Position{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	exe, err := MaterializeShellAction(dir, a, bound, operands)
	if err != nil {
		t.Fatal(err)
	}
	return exe, dir
}

func TestMaterializeShellActionKeepsBoundValuesOutOfArgv(t *testing.T) {
	const secret = "s3cr3t-token"
	exe, dir := materializeForTest(t, bindingScript, map[string]string{
		"session_name": secret,
		"send_text":    "tmux send-keys -t " + secret,
	}, "ready")

	for _, arg := range append([]string{exe.Path}, exe.Operands...) {
		if strings.Contains(arg, secret) {
			t.Errorf("a bound value reached argv: %q", arg)
		}
	}
	if len(exe.Operands) != 1 || exe.Operands[0] != "ready" {
		t.Errorf("an operand is passed positionally: got %q", exe.Operands)
	}
	wrapper, err := os.ReadFile(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wrapper), secret) {
		t.Errorf("a bound value reached the generated wrapper: %q", wrapper)
	}
}

// TestMaterializeShellActionTakesItsSourceFromNoVariable is the other half of
// "a bound value is data": nothing a binding or an ambient variable can set
// may decide which shell source runs.
func TestMaterializeShellActionTakesItsSourceFromNoVariable(t *testing.T) {
	tmp := t.TempDir()
	elsewhere := filepath.Join(tmp, "elsewhere.sh")
	if err := os.WriteFile(elsewhere, []byte("printf ELSEWHERE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, _ := materializeForTest(t, "printf AUTHORS-SCRIPT\n", map[string]string{
		"session_name": elsewhere,
		"send_text":    elsewhere,
	})

	cmd := exec.Command(exe.Path, exe.Operands...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PLECT_SHELL_SCRIPT=" + elsewhere,
		"PLECT_SHELL_BINDINGS=" + elsewhere,
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "AUTHORS-SCRIPT" {
		t.Errorf("an ambient variable chose the source that ran: got %q", out)
	}
}

// TestMaterializeShellActionIgnoresABindKeyNamedLikeAControlVariable is the
// regression test for the transport's one escape: a binding once shadowed the
// variable the wrapper read its script path from, so a bound value chose the
// source that ran. No bind key is refused for its name — nothing reads one.
func TestMaterializeShellActionIgnoresABindKeyNamedLikeAControlVariable(t *testing.T) {
	tmp := t.TempDir()
	elsewhere := filepath.Join(tmp, "elsewhere.sh")
	if err := os.WriteFile(elsewhere, []byte("printf ELSEWHERE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := ParseAction(map[string]any{
		"type":   "shell",
		"script": "printf AUTHORS-SCRIPT\n",
		"bind": map[string]any{
			"PLECT_SHELL_SCRIPT":   map[string]any{"from": "event.body"},
			"PLECT_SHELL_BINDINGS": map[string]any{"from": "event.body"},
		},
	}, Position{})
	if err != nil {
		t.Fatal(err)
	}
	exe, err := MaterializeShellAction(filepath.Join(tmp, "run"), a, map[string]string{
		"PLECT_SHELL_SCRIPT":   elsewhere,
		"PLECT_SHELL_BINDINGS": elsewhere,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe.Path, exe.Operands...).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "AUTHORS-SCRIPT" {
		t.Errorf("a bound value chose the shell source that ran: got %q", out)
	}
}

func TestMaterializeShellActionWritesAPrivateBindingFile(t *testing.T) {
	_, dir := materializeForTest(t, bindingScript, map[string]string{"session_name": "s", "send_text": "true"})

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("run directory mode: got %o, want 700", perm)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawBindings bool
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			t.Errorf("%s is readable beyond its owner: mode %o", entry.Name(), perm)
		}
		if strings.Contains(entry.Name(), "bindings") {
			sawBindings = true
			if perm != 0o600 {
				t.Errorf("the binding file is mode 0600, got %o", perm)
			}
		}
	}
	if !sawBindings {
		t.Error("no binding file was written")
	}
}

func TestMaterializeShellActionKeepsTheScriptLiteral(t *testing.T) {
	const script = "echo \"$session_name\" | tr a-z A-Z\n"
	_, dir := materializeForTest(t, script, map[string]string{"session_name": "s", "send_text": "true"})

	got, err := os.ReadFile(filepath.Join(dir, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != script {
		t.Errorf("the author's shell source is executed byte for byte:\n got %q\nwant %q", got, script)
	}
}

// TestMaterializeShellActionRoundTripsHostileValues is the transport's
// central claim: a bound value is data, so a value that would be shell
// syntax if it were interpolated into the source arrives intact instead.
func TestMaterializeShellActionRoundTripsHostileValues(t *testing.T) {
	hostile := map[string]string{
		"session_name": `'; touch /tmp/pwned; echo '`,
		"send_text":    "a\"b$c`d`\\e\nf",
	}
	exe, _ := materializeForTest(t, bindingScript, hostile, "ready $(id)")

	cmd := exec.Command(exe.Path, exe.Operands...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := hostile["session_name"] + "|" + hostile["send_text"] + "|ready $(id)"
	if string(out) != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
}

func TestMaterializeShellActionLeavesAnUnboundKeyUnassigned(t *testing.T) {
	a, err := ParseAction(map[string]any{
		"type":   "shell",
		"script": "true\n",
		"bind": map[string]any{
			"session_name": map[string]any{"from": "session.name"},
			"workspace":    map[string]any{"from": "workspace.dir", "optional": true},
		},
	}, Position{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	if _, err := MaterializeShellAction(dir, a, map[string]string{"session_name": "s1"}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bindings.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "session_name='s1'\n"; string(raw) != want {
		t.Errorf("bindings = %q, want %q — an unresolved key assigns nothing", raw, want)
	}
}

func TestMaterializeShellActionRejectsAnExecAction(t *testing.T) {
	a, err := ParseAction(map[string]any{"type": "exec", "bin": "okf-goal"}, Position{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeShellAction(filepath.Join(t.TempDir(), "run"), a, nil, nil); err == nil {
		t.Error("the binding transport is a shell action's; an exec action has no shell source")
	}
}
