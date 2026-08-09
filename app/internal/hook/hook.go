package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/hook"
)

// HookPoint represents when a hook should be executed.
type HookPoint string

// PostSyncChange is the only surviving hook point. Lifecycle hooks
// (post_add/post_resume/pre_remove) moved to effect setup/cleanup, and
// hookrunner — the last consumer — is gone. It fires only from the legacy
// ghcache Sync path (tws watch / ls --sync / show --sync) and retires with it;
// don't add new hook points or new callers.
const (
	PostSyncChange HookPoint = "post_sync_change"
)

// Vars is an alias for the shared contract type.
type Vars = contract.Input

// Run executes all hooks for the given hook point.
// The command is executed via "bash -c" and Vars are passed as JSON via stdin.
// If a hook fails, the error is returned but remaining hooks still execute.
// Returns nil if no hooks are configured.
func Run(hookPoint HookPoint, hooks []config.HookConfig, vars Vars) error {
	if len(hooks) == 0 {
		return nil
	}

	jsonPayload, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("hook %s: failed to marshal vars to JSON: %w", hookPoint, err)
	}

	var firstErr error
	for i, h := range hooks {
		cmd := exec.Command("bash", "-c", h.Command)
		cmd.Stdin = strings.NewReader(string(jsonPayload))
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if vars.WorktreePath != "" {
			if _, err := os.Stat(vars.WorktreePath); err == nil {
				cmd.Dir = vars.WorktreePath
			}
		}

		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "hook %s[%d]: command failed: %v\n", hookPoint, i, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("hook %s[%d]: command failed: %w", hookPoint, i, err)
			}
		}
	}

	return firstErr
}
