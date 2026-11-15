package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ratebudget"
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
	err := runGhAPI(guard, nil, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
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
	err := runGhAPI(guard, nil, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
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
	err := runGhAPI(guard, nil, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y"})
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
	err := runGhAPI(guard, nil, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y/commits/abc/check-runs", "--paginate", "--jq", "."})
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
	err := runGhAPI(guard, nil, strings.NewReader(""), &stdout, &stderr, []string{"repos/x/y/commits/abc/check-runs", "--paginate"})
	if err != nil {
		t.Fatalf("runGhAPI: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"ok":true}` {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestCmdGhAPI_PluginCacheRoutesGuardState(t *testing.T) {
	fakeGhBin(t, `printf 'HTTP/1.1 200 OK\r\n\r\nok'`)
	dir := t.TempDir()
	t.Setenv("PLECT_CACHE_DIR", dir)
	if err := cmdGhAPI([]string{"repos/x/y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rate-budget.json")); err != nil {
		t.Errorf("expected rate-budget.json under PLECT_CACHE_DIR, got: %v", err)
	}
}

func TestCmdGhAPI_AppAuthExportsGHTokenToGhChild(t *testing.T) {
	tokenSrv := fakeInstallationTokenServer(t, "ghs_gh_api")
	defer tokenSrv.Close()

	fakeGhBin(t, `printf 'HTTP/1.1 200 OK\r\n\r\n'; echo "GH_TOKEN=$GH_TOKEN" >&2`)

	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	t.Setenv(envBaseURL, tokenSrv.URL)

	dir := t.TempDir()
	t.Setenv("PLECT_CACHE_DIR", dir)
	// Capture stderr, where the fake gh echoed GH_TOKEN, by redirecting the
	// process's own stderr — cmdGhAPI writes straight to os.Stderr.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	cmdErr := cmdGhAPI([]string{"repos/x/y"})
	os.Stderr = origStderr
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if cmdErr != nil {
		t.Fatalf("cmdGhAPI: %v (stderr=%s)", cmdErr, buf.String())
	}
	if !strings.Contains(buf.String(), "GH_TOKEN=ghs_gh_api") {
		t.Errorf("gh child stderr = %q, want it to have seen GH_TOKEN=ghs_gh_api", buf.String())
	}
}

func TestCmdGhAPI_AppAuthMintFailureFailsLoudWithoutCallingGh(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer tokenSrv.Close()

	callLog := filepath.Join(t.TempDir(), "calls.log")
	fakeGhBin(t, `echo "must not be invoked" >> `+callLog)

	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	t.Setenv(envBaseURL, tokenSrv.URL)

	t.Setenv("PLECT_CACHE_DIR", t.TempDir())
	err := cmdGhAPI([]string{"repos/x/y"})
	if err == nil {
		t.Fatal("want an error when the App token mint fails")
	}
	if _, statErr := os.Stat(callLog); !os.IsNotExist(statErr) {
		t.Fatalf("gh must not be invoked when the mint fails, stat err=%v", statErr)
	}
}

func TestCmdGhAPIRequiresPluginCache(t *testing.T) {
	t.Setenv("PLECT_CACHE_DIR", "")
	if err := cmdGhAPI([]string{"repos/x/y"}); err == nil || !strings.Contains(err.Error(), "PLECT_CACHE_DIR") {
		t.Fatalf("error = %v", err)
	}
}
