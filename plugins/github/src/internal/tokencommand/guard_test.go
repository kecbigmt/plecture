package tokencommand

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardCommandTokenAndDeny(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "token helper")
	count := filepath.Join(dir, "count")
	gh := filepath.Join(dir, "gh")
	for path, script := range map[string]string{
		command: "#!/bin/sh\necho called >> '" + count + "'\nprintf 'secret-token\\n'\n",
		gh:      "#!/bin/sh\ncase \"$*\" in *secret-token*) exit 9;; esac\nprintf '%s\\n' \"$GH_TOKEN\"\n",
	} {
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	commandFile := filepath.Join(dir, "token-command.json")
	if err := os.WriteFile(commandFile, []byte(`["`+command+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join("..", "..", "..", "scripts", "gh-app-guard")
	run := func(args ...string) (string, error) {
		cmd := exec.Command(guard, args...)
		cmd.Env = append(os.Environ(), "GH_GUARD_REAL_GH="+gh, "GH_APP_GUARD_TOKEN_BIN="+command, "GH_APP_GUARD_TOKEN_COMMAND_FILE="+commandFile)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("pr", "merge", "1"); err == nil || strings.Contains(out, "secret-token") {
		t.Fatalf("denial: %q, %v", out, err)
	}
	if _, err := os.Stat(count); !os.IsNotExist(err) {
		t.Fatalf("command called on denial: %v", err)
	}
	for i := 0; i < 2; i++ {
		if out, err := run("issue", "view", "1"); err != nil || out != "secret-token\n" {
			t.Fatalf("allowed: %q, %v", out, err)
		}
	}
	data, err := os.ReadFile(count)
	if err != nil || string(data) != "called\ncalled\n" {
		t.Fatalf("calls: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".token-cache.json")); !os.IsNotExist(err) {
		t.Fatalf("token cache: %v", err)
	}
}

func TestGuardRejectsCommandFailuresWithoutLeakingOutput(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "token helper")
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	commandFile := filepath.Join(dir, "token-command.json")
	if err := os.WriteFile(commandFile, []byte(`["`+command+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ script, status string }{
		{"printf 'secret-token\\n'; echo secret-token >&2; exit 7", "exit status 7"},
		{"exit 0", "exit status 0"},
		{"printf 'secret-token\\nsecond\\n'", "exit status 0"},
		{"printf 'secret-token'; head -c 8192 /dev/zero | tr '\\000' x", "exit status 0"},
	} {
		if err := os.WriteFile(command, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(filepath.Join("..", "..", "..", "scripts", "gh-app-guard"), "issue", "view", "1")
		cmd.Env = append(os.Environ(), "GH_GUARD_REAL_GH="+gh, "GH_APP_GUARD_TOKEN_BIN="+command, "GH_APP_GUARD_APP_ID=1", "GH_APP_GUARD_TOKEN_COMMAND_FILE="+commandFile)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), command) || !strings.Contains(string(out), tc.status) || strings.Contains(string(out), "secret-token") {
			t.Fatalf("script %q: output %q, error %v", tc.script, out, err)
		}
	}
}
