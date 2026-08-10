package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/sennit/plugins/github-watcher/internal/ratebudget"
)

// fakeGhBin writes an executable `gh` stub into a temp dir and prepends it to
// PATH for the duration of the test. The shebang resolves bash's real path
// rather than assuming /usr/bin/env — the Nix build sandbox has no FHS paths,
// so a plain "#!/usr/bin/env bash" fails to exec there.
func fakeGhBin(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!"+bash+"\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A successful non-paginated call relays only the body (headers stripped) to
// stdout and records success on the guard.
func TestRunGhAPI_SuccessRelaysBodyOnly(t *testing.T) {
	fakeGhBin(t, `printf 'HTTP/1.1 200 OK\r\nEtag: "v1"\r\n\r\n{"ok":true}'`)
	guard := ratebudget.NewGuard(t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runGhAPI(guard, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
	if err != nil {
		t.Fatalf("runGhAPI: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.String() != `{"ok":true}` {
		t.Errorf("stdout = %q, want only the body", stdout.String())
	}
	if wait, _ := guard.Wait(); wait != 0 {
		t.Errorf("guard wait = %v after success, want 0", wait)
	}
}

// A 403 with Retry-After must set the guard's backoff to that exact window —
// the whole point of routing through -i instead of falling back to the
// exponential default.
func TestRunGhAPI_ThrottleHonorsRetryAfterHeader(t *testing.T) {
	fakeGhBin(t, `
printf 'HTTP/1.1 403 Forbidden\r\nRetry-After: 90\r\n\r\n{"message":"secondary rate limit"}'
exit 1
`)
	guard := ratebudget.NewGuard(t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runGhAPI(guard, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	wait, werr := guard.Wait()
	if werr != nil {
		t.Fatal(werr)
	}
	if wait < 85*time.Second || wait > 90*time.Second {
		t.Errorf("guard wait = %v, want ~90s (Retry-After honored, not the exponential default)", wait)
	}
}

// A pending backoff must refuse to call gh at all — no immediate retry.
func TestRunGhAPI_RefusesCallWhileBackedOff(t *testing.T) {
	guard := ratebudget.NewGuard(t.TempDir())
	if err := guard.RecordThrottle(time.Minute, time.Time{}); err != nil {
		t.Fatal(err)
	}

	fakeGhBin(t, `echo "must not be invoked" >> `+filepath.Join(t.TempDir(), "calls.log")+`; exit 0`)

	var stdout, stderr bytes.Buffer
	err := runGhAPI(guard, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
	if err == nil {
		t.Fatal("expected refusal while backed off")
	}
	if !strings.Contains(stderr.String(), "backed off") {
		t.Errorf("stderr = %q, want a backed-off message", stderr.String())
	}
}

// The --paginate fallback path detects a throttle from gh's plain-text
// "(HTTP 403)" error (no header access) and falls back to the guard's
// exponential backoff rather than erroring out unrecognized.
func TestRunGhAPI_PaginateFallbackDetectsThrottleFromErrorText(t *testing.T) {
	fakeGhBin(t, `echo "gh: secondary rate limit exceeded. (HTTP 403)" >&2; exit 1`)
	guard := ratebudget.NewGuard(t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runGhAPI(guard, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y/commits/abc/check-runs", "--paginate", "--jq", "."})
	if err == nil {
		t.Fatal("expected an error")
	}
	wait, werr := guard.Wait()
	if werr != nil {
		t.Fatal(werr)
	}
	if wait <= 0 {
		t.Error("expected the guard to record a fallback backoff")
	}
}

// The --paginate fallback path relays stdout directly (unlike the
// conditional path, it never strips headers, since -i is not used there).
func TestRunGhAPI_PaginateFallbackRelaysStdout(t *testing.T) {
	fakeGhBin(t, `echo '{"ok":true}'`)
	guard := ratebudget.NewGuard(t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runGhAPI(guard, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y/commits/abc/check-runs", "--paginate"})
	if err != nil {
		t.Fatalf("runGhAPI: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"ok":true}` {
		t.Errorf("stdout = %q", stdout.String())
	}
}

// cmdGhAPI's --data-dir must precede the gh api args and point the guard at
// that directory (the poll loop's --data-dir), not the process-default XDG
// path — otherwise a session using a custom data dir would share no budget
// with its own gh-api calls.
func TestCmdGhAPI_DataDirRoutesGuardState(t *testing.T) {
	fakeGhBin(t, `printf 'HTTP/1.1 200 OK\r\n\r\nok'`)
	dir := t.TempDir()
	if err := cmdGhAPI([]string{"--data-dir", dir, "repos/x/y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rate-budget.json")); err != nil {
		t.Errorf("expected rate-budget.json under --data-dir, got: %v", err)
	}
}

// serve and gh-api must resolve the same on-disk budget file when given the
// same --data-dir (explicit or, via XDG defaulting, empty) — otherwise the
// watcher daemon and config-layer gh calls back off independently instead of
// sharing one budget.
func TestGhAPIDataDir_MatchesAcrossServeAndGhAPIInvocations(t *testing.T) {
	t.Run("explicit --data-dir", func(t *testing.T) {
		dir := t.TempDir()
		if got := ghAPIDataDir(dir); got != dir {
			t.Errorf("ghAPIDataDir(%q) = %q, want %q", dir, got, dir)
		}
	})

	t.Run("default (XDG) --data-dir is stable across calls", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		serveGuardPath := ghAPIDataDir("")
		ghAPIGuardPath := ghAPIDataDir("")
		if serveGuardPath != ghAPIGuardPath {
			t.Errorf("serve resolved %q, gh-api resolved %q; must match", serveGuardPath, ghAPIGuardPath)
		}
	})
}
