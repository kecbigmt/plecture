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

func cmdGhAPI(args []string) error {
	cacheDir := os.Getenv("PLECT_CACHE_DIR")
	if cacheDir == "" {
		return fmt.Errorf("PLECT_CACHE_DIR is required")
	}
	guard := ratebudget.NewGuard(cacheDir)
	tokenFn, err := appAuthTokenFunc(cacheDir)
	if err != nil {
		return err
	}
	return runGhAPI(guard, tokenFn, os.Stdin, os.Stdout, os.Stderr, args)
}

// runGhAPI runs `gh api` gated by the shared rate budget: a pending backoff
// refuses the call outright (no immediate retry), and a 403/429 response
// extends the backoff — using the real Retry-After/X-RateLimit-Reset headers
// when available, an exponential fallback otherwise.
func runGhAPI(guard *ratebudget.Guard, tokenFunc func() (string, error), stdin io.Reader, stdout, stderr io.Writer, args []string) error {
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
		return runGhAPIPlain(guard, tokenFunc, stdin, stdout, stderr, args)
	}
	return runGhAPIConditional(guard, tokenFunc, stdin, stdout, stderr, args)
}

func ghEnv(tokenFunc func() (string, error)) ([]string, error) {
	if tokenFunc == nil {
		return nil, nil
	}
	token, err := tokenFunc()
	if err != nil {
		return nil, fmt.Errorf("github app token: %w", err)
	}
	return append(os.Environ(), "GH_TOKEN="+token), nil
}

// runGhAPIConditional runs `gh api -i <args>`, relays only the body to
// stdout, and reports 403/429 to guard using the real Retry-After/
// X-RateLimit-Reset headers when GitHub sends them.
func runGhAPIConditional(guard *ratebudget.Guard, tokenFunc func() (string, error), stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	env, err := ghEnv(tokenFunc)
	if err != nil {
		return err
	}
	c := exec.Command("gh", append([]string{"api"}, append(args, "-i")...)...)
	c.Env = env
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
func runGhAPIPlain(guard *ratebudget.Guard, tokenFunc func() (string, error), stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	env, err := ghEnv(tokenFunc)
	if err != nil {
		return err
	}
	c := exec.Command("gh", append([]string{"api"}, args...)...)
	c.Env = env
	c.Stdin = stdin
	c.Stdout = stdout
	var stderrBuf bytes.Buffer
	c.Stderr = io.MultiWriter(stderr, &stderrBuf)
	err = c.Run()
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
