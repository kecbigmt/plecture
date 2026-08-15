package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ratebudget"
)

// cmdGhAPI is `github-watcher gh-api [--data-dir <dir>] <gh api args...>`:
// the cross-process helper the github config layer (providers/github.toml,
// resources/github.toml) invokes instead of calling `gh api` directly, so
// its setup/observe scripts share the same rate budget as the poll loop —
// a 403/429 seen by either blocks the other until the backoff window
// elapses, instead of each retrying independently and re-tripping the
// secondary rate limit. --data-dir, when given, must come first and must
// match the poll loop's own --data-dir; there is no other flag of gh-api's
// own to parse, since every remaining argument belongs to `gh api` and must
// reach it unexamined (gh's own flags must never be parsed by plect code).
func cmdGhAPI(args []string) error {
	dataDir := ""
	if len(args) >= 2 && args[0] == "--data-dir" {
		dataDir, args = args[1], args[2:]
	}
	guard := ratebudget.NewGuard(ghAPIDataDir(dataDir))
	return runGhAPI(guard, os.Stdin, os.Stdout, os.Stderr, args)
}

// ghAPIDataDir mirrors NewStore's default so `github-watcher gh-api` and
// `github-watcher serve` share one budget file without every config-layer
// caller having to pass --data-dir explicitly.
func ghAPIDataDir(dataDir string) string {
	if dataDir != "" {
		return dataDir
	}
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		home, _ := os.UserHomeDir()
		xdgDataHome = home + "/.local/share"
	}
	return xdgDataHome + "/github-watcher"
}

// runGhAPI runs `gh api` gated by the shared rate budget: a pending backoff
// refuses the call outright (no immediate retry), and a 403/429 response
// extends the backoff — using the real Retry-After/X-RateLimit-Reset headers
// when available, an exponential fallback otherwise.
func runGhAPI(guard *ratebudget.Guard, stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	if wait, err := guard.Wait(); err == nil && wait > 0 {
		fmt.Fprintf(stderr, "github-watcher gh-api: shared rate budget backed off for %s; refusing to call gh (no immediate retry)\n", wait.Round(time.Second))
		return fmt.Errorf("gh rate budget backed off for %s", wait.Round(time.Second))
	}

	// --paginate can emit several concatenated `-i` response blocks (one per
	// page), which the single-response parser below doesn't split apart.
	// Those calls fall back to a plain passthrough with text-based throttle
	// detection (no precise Retry-After/reset) rather than risk corrupting a
	// paginated caller's stdout.
	if slices.Contains(args, "--paginate") {
		return runGhAPIPlain(guard, stdin, stdout, stderr, args)
	}
	return runGhAPIConditional(guard, stdin, stdout, stderr, args)
}

// runGhAPIConditional runs `gh api -i <args>`, relays only the body to
// stdout, and reports 403/429 to guard using the real Retry-After/
// X-RateLimit-Reset headers when GitHub sends them.
func runGhAPIConditional(guard *ratebudget.Guard, stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	c := exec.Command("gh", append([]string{"api"}, append(args, "-i")...)...)
	c.Stdin = stdin
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = stderr
	runErr := c.Run()

	resp, ok := ratebudget.ParseHTTPResponse(buf.Bytes())
	if !ok {
		// Not a parseable HTTP response at all (e.g. gh failed to start) —
		// relay whatever came back and the original error.
		stdout.Write(buf.Bytes())
		return runErr
	}

	switch {
	case resp.Status == 403 || resp.Status == 429:
		retryAfter := ratebudget.RetryAfterSeconds(resp.Headers["retry-after"])
		reset := ratebudget.RateLimitReset(resp.Headers["x-ratelimit-reset"])
		if err := guard.RecordThrottle(retryAfter, reset); err != nil {
			fmt.Fprintf(stderr, "github-watcher gh-api: record throttle failed: %v\n", err)
		}
		stderr.Write(resp.Body)
		return fmt.Errorf("gh api: HTTP %d", resp.Status)
	case resp.Status >= 200 && resp.Status < 300:
		if err := guard.RecordSuccess(); err != nil {
			fmt.Fprintf(stderr, "github-watcher gh-api: record success failed: %v\n", err)
		}
		stdout.Write(resp.Body)
		return nil
	default:
		stderr.Write(resp.Body)
		return fmt.Errorf("gh api: HTTP %d", resp.Status)
	}
}

// runGhAPIPlain is the --paginate fallback: a straight passthrough with
// throttle detection from gh's plain-text error only (no header access, so
// the guard falls back to its exponential backoff rather than GitHub's exact
// Retry-After/reset window).
func runGhAPIPlain(guard *ratebudget.Guard, stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	c := exec.Command("gh", append([]string{"api"}, args...)...)
	c.Stdin = stdin
	c.Stdout = stdout
	var stderrBuf bytes.Buffer
	c.Stderr = io.MultiWriter(stderr, &stderrBuf)
	err := c.Run()
	if err != nil {
		if isThrottleResponse(stderrBuf.String()) {
			if rerr := guard.RecordThrottle(0, time.Time{}); rerr != nil {
				fmt.Fprintf(stderr, "github-watcher gh-api: record throttle failed: %v\n", rerr)
			}
		}
		return err
	}
	if rerr := guard.RecordSuccess(); rerr != nil {
		fmt.Fprintf(stderr, "github-watcher gh-api: record success failed: %v\n", rerr)
	}
	return nil
}

// httpStatusRE matches gh's "(HTTP 403)"-style error suffix.
var httpStatusRE = regexp.MustCompile(`\(HTTP (\d+)\)`)

func isThrottleResponse(stderr string) bool {
	m := httpStatusRE.FindStringSubmatch(stderr)
	if m == nil {
		return false
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	return code == 403 || code == 429
}
