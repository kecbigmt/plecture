package task

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// SubscribeHookVars is the context a workspace provider's
// `subscribe` hook. Like WorkflowHookVars it is deliberately minimal —
// subscribe is a runtime verb that runs from an arbitrary cwd with no
// workspace or task DAG in scope. It forwards only the current session and
// the opaque resource being subscribed, plus the mounted plugin list so the
// hook can name its own plugin's executables through `bin`.
type SubscribeHookVars struct {
	ResourceID  string
	SessionName string
	Plugins     []plugins.Mounted
	// SourcePath is the workspace provider definition's own file path
	// (config.WorkspaceProviderConfig.SourcePath), threaded through so a
	// `bin = "<name>"` in Subscribe can resolve against the workspace
	// provider's containing plugin.
	SourcePath string
}

// RunWorkspaceProviderSubscribe renders and runs the workspace provider's
// `subscribe` hook. The hook is fire-and-forget: it has no outputs contract
// and no persisted state — it binds the current session to the resource in
// whatever delivery mechanism the workspace provider owns (a resident
// watcher's subscription registry, say). stderr is folded into the error on
// failure for diagnosis.
func RunWorkspaceProviderSubscribe(prov config.WorkspaceProviderConfig, vars SubscribeHookVars) error {
	if prov.Subscribe == nil {
		return fmt.Errorf("workspace provider %q declares no subscribe hook", prov.ID)
	}
	env := lang.Environment{
		"session":  map[string]any{"name": vars.SessionName},
		"resource": map[string]any{"id": vars.ResourceID},
	}
	_, stderr, runErr := runProviderAction(prov.Subscribe, providerEval(env, vars.Plugins, vars.SourcePath, prov.Ownership()))
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return fmt.Errorf("workspace provider %q subscribe: %w: %s", prov.ID, runErr, msg)
		}
		return fmt.Errorf("workspace provider %q subscribe: %w", prov.ID, runErr)
	}
	return nil
}
