package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets a test hand the compiled test binary to git as the credential
// helper, so git itself parses what the helper prints.
func TestMain(m *testing.M) {
	if os.Getenv("GH_APP_TOKEN_TEST_RUN_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeTokenCommand(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "installation token")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]string{path, "get"})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestCmdCredential_TokenCommandSuppliesThePasswordWithoutAppKeyFlags(t *testing.T) {
	command := fakeTokenCommand(t, `[ "$1" = get ] || exit 9; printf 'ghs_from_command\n'`)

	var out bytes.Buffer
	err := cmdCredential([]string{"--token-command", command, "get"}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out)
	if err != nil {
		t.Fatalf("cmdCredential: %v", err)
	}
	if got, want := out.String(), "username=x-access-token\npassword=ghs_from_command\n\n"; got != want {
		t.Errorf("credential output = %q, want %q", got, want)
	}
}

func TestCmdCredential_TokenCommandFailureNamesTheCommandButNotItsOutput(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"non-zero exit", "printf 'ghs_leaked\\n'; echo ghs_leaked >&2; exit 3", "exit status 3"},
		{"empty output", "exit 0", "invalid output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := fakeTokenCommand(t, tc.script)
			var out bytes.Buffer
			err := cmdCredential([]string{"--token-command", command, "get"}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out)
			if err == nil || !strings.Contains(err.Error(), "installation token") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming the command and %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "ghs_leaked") || out.Len() != 0 {
				t.Errorf("token leaked: error %q, stdout %q", err, out.String())
			}
		})
	}
}

func TestCmdCredential_TokenCommandAndPrivateKeyPathAreMutuallyExclusive(t *testing.T) {
	command := fakeTokenCommand(t, "printf 'ghs_from_command\\n'")
	var out bytes.Buffer
	err := cmdCredential([]string{
		"--token-command", command,
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", "/etc/plect/gh-app.pem",
		"--cache-path", filepath.Join(t.TempDir(), "cache.json"),
		"get",
	}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want a mutual-exclusion error", err)
	}
}

func TestCmdPrint_RejectsTokenCommand(t *testing.T) {
	command := fakeTokenCommand(t, "printf 'ghs_from_command\\n'")
	var out bytes.Buffer
	if err := cmdPrint([]string{"--token-command", command}, &out); err == nil {
		t.Fatalf("cmdPrint accepted --token-command and printed %q", out.String())
	}
}

func TestCmdCredential_GitReceivesTheTokenCommandsToken(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fakeTokenCommand(t, "printf 'ghs_for_git_from_command\\n'")
	helper := "!'" + self + "' 'credential' '--token-command' '" + command + "'"
	cmd := exec.Command("git",
		"-c", "credential.helper=",
		"-c", "credential.https://github.com.helper="+helper,
		"credential", "fill")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "GH_APP_TOKEN_TEST_RUN_MAIN=1", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=acme/widgets.git\n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git credential fill: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "password=ghs_for_git_from_command\n") || !strings.Contains(string(out), "username=x-access-token\n") {
		t.Errorf("git credential fill output lacks the command's token:\n%s", out)
	}
}
