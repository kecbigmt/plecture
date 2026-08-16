package busservice

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

// runProcess starts decl's executable and blocks until it exits or ctx is
// cancelled. It records the running pid via onStart before waiting, so a
// caller observes "running" status for the whole process lifetime, not just
// after the fact. On ctx cancellation, the child's whole process group gets
// SIGTERM (a service may spawn helpers of its own); WaitDelay bounds how
// long Wait keeps waiting for the group to actually exit before the
// os/exec package escalates to SIGKILL, so a service that ignores SIGTERM
// cannot hang bus shutdown indefinitely.
func runProcess(ctx context.Context, decl Declaration, waitDelay time.Duration, onStart func(pid int)) error {
	cmd := exec.CommandContext(ctx, decl.ExecPath, decl.Args...)
	cmd.Env = buildEnv(decl.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return err
	}
	onStart(cmd.Process.Pid)
	return cmd.Wait()
}

// buildEnv merges decl.Env on top of the supervisor's own environment,
// de-duplicated and sorted so a key declared in both places has one
// unambiguous value regardless of libc environ merge behavior. Secrets
// (e.g. a Slack bot token) are expected to already be present in the
// supervisor's own environment — plugin.toml's `env` field carries only
// non-secret literals, per the plugin service lifecycle ADR.
func buildEnv(overrides map[string]string) []string {
	merged := make(map[string]string, len(overrides))
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range overrides {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + merged[k]
	}
	return out
}

// missingRequiredEnv returns the subset of required not already set in the
// supervisor's own environment.
func missingRequiredEnv(required []string) []string {
	var missing []string
	for _, name := range required {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
