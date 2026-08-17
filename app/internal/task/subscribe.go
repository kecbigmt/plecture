package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// SubscribeHookVars is the template surface for a workspace provider's
// `subscribe` hook. Like WorkflowHookVars it is deliberately minimal —
// subscribe is a runtime verb that runs from an arbitrary cwd with no
// workspace or task DAG in scope. It forwards only the current session and
// the opaque resource being subscribed, plus the mounted plugin list so the
// hook can invoke its own plugin's executables through `{{bin ...}}`.
type SubscribeHookVars struct {
	ResourceID  string
	SessionName string
	Plugins     []plugins.Mounted
	// SourcePath is the workspace provider definition's own file path
	// (config.WorkspaceProviderConfig.SourcePath), threaded through so a
	// `{{bin "<name>"}}` in Subscribe can resolve against the workspace
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
	if strings.TrimSpace(prov.Subscribe) == "" {
		return fmt.Errorf("workspace provider %q declares no subscribe hook", prov.ID)
	}
	cmdStr, err := renderSubscribeHook(prov.Subscribe, vars)
	if err != nil {
		return fmt.Errorf("workspace provider %q subscribe template: %w", prov.ID, err)
	}
	_, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return fmt.Errorf("workspace provider %q subscribe: %w: %s", prov.ID, runErr, msg)
		}
		return fmt.Errorf("workspace provider %q subscribe: %w", prov.ID, runErr)
	}
	return nil
}

// renderSubscribeHook renders a subscribe hook template. Strict
// (missingkey=error) like setup: the surface is a fixed struct so a missing
// key is an author typo, not a runtime-empty value.
func renderSubscribeHook(cmd string, vars SubscribeHookVars) (string, error) {
	// bin is built per render call, not part of the static templateFuncs map,
	// because it resolves against this render's own vars.Plugins — mirrors
	// renderWith's and renderWorkflowHook's dynamicFuncs for the same reason.
	dynamicFuncs := template.FuncMap{
		"bin": func(ref string) (string, error) {
			return plugins.ResolveBin(vars.Plugins, vars.SourcePath, ref)
		},
	}
	tmpl, err := template.New("subscribe_hook").
		Option("missingkey=error").
		Funcs(templateFuncs).
		Funcs(dynamicFuncs).
		Parse(cmd)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return buf.String(), nil
}
