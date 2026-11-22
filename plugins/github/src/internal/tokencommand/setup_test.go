package tokencommand

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestGuardSetupRequiresExactlyOneCredentialSource(t *testing.T) {
	var config struct {
		Guard struct {
			Setup struct {
				Script string `toml:"script"`
			} `toml:"setup"`
		} `toml:"gh_app_guard"`
	}
	if _, err := toml.DecodeFile(filepath.Join("..", "..", "..", "config", "tasks", "gh_app_guard.toml"), &config); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "setup.sh")
	if err := os.WriteFile(path, []byte(config.Guard.Setup.Script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, key, command string }{
		{"neither", "", ""},
		{"both", "/private/key", `["/host/token"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", path)
			cmd.Env = append(os.Environ(), "private_key_path="+tc.key, "token_command="+tc.command)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "private_key_path") || !strings.Contains(string(out), "token_command") {
				t.Fatalf("output %q, error %v", out, err)
			}
		})
	}
}

func TestGuardSetupCommandNeedsNoAppIdentifiers(t *testing.T) {
	var config struct {
		Guard struct {
			Setup struct {
				Script string `toml:"script"`
			} `toml:"setup"`
		} `toml:"gh_app_guard"`
	}
	if _, err := toml.DecodeFile(filepath.Join("..", "..", "..", "config", "tasks", "gh_app_guard.toml"), &config); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for path := range map[string]bool{filepath.Join(dir, "gh"): true, filepath.Join(dir, "app-guard"): true} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", config.Guard.Setup.Script)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TMPDIR="+dir,
		"app_id=", "installation_id=", "owner=", "repo=", "private_key_path=",
		"token_command=[\"/host/token\"]", "token_bin="+filepath.Join(dir, "app-guard"),
		"app_guard_bin="+filepath.Join(dir, "app-guard"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: %s: %v", out, err)
	}
}
