// Package ghapi runs `gh api` calls on behalf of the provider, either
// directly or through a github-watcher binary that gates calls against its
// poll loop's shared rate budget — the same cross-process coupling
// production's provider/resource scripts rely on, so a setup or observe call
// and the watcher's own polling back off together instead of each retrying
// independently and re-tripping GitHub's secondary rate limit.
package ghapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/procexec"
)

// Client runs one GitHub REST call per JSON invocation.
type Client struct {
	// Program is the executable to run.
	Program string
	// Args are the fixed arguments before the caller's own, selecting the
	// subcommand.
	Args []string
	// Runner executes the child process. Defaults to procexec.Default.
	Runner procexec.Runner
}

// Direct calls `gh api` directly, without a shared rate budget.
func Direct() *Client {
	return &Client{Program: "gh", Args: []string{"api"}}
}

// ViaWatcher calls `<watcherPath> gh-api`, sharing the named github-watcher
// binary's poll-loop rate budget.
func ViaWatcher(watcherPath string) *Client {
	return &Client{Program: watcherPath, Args: []string{"gh-api"}}
}

// JSON runs the API call and returns the JSON body `gh api` already unwraps
// from the HTTP response.
func (c *Client) JSON(ctx context.Context, args ...string) ([]byte, error) {
	runner := c.Runner
	if runner == nil {
		runner = procexec.Default
	}
	full := append(append([]string{}, c.Args...), args...)
	out, stderr, err := runner.Run(ctx, "", false, c.Program, full...)
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("%s %s: %s", c.Program, strings.Join(full, " "), msg)
		}
		return nil, fmt.Errorf("%s %s: %w", c.Program, strings.Join(full, " "), err)
	}
	return out, nil
}
