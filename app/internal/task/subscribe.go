package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/kecbigmt/sennit/app/internal/config"
)

// SubscribeHookVars is the template surface for a provider's `subscribe` hook.
// Like WorkflowHookVars it is deliberately minimal — subscribe is a runtime
// verb that runs from an arbitrary cwd with no working directory or task DAG
// in scope. It forwards only the current session and the opaque resource
// being subscribed.
type SubscribeHookVars struct {
	ResourceID  string
	SessionName string
}

// RunProviderSubscribe renders and runs the provider's `subscribe` hook. The
// hook is fire-and-forget: it has no outputs contract and no persisted state —
// it binds the current session to the resource in whatever delivery mechanism
// the provider owns (a resident watcher's subscription registry, say).
// stderr is folded into the error on failure for diagnosis.
func RunProviderSubscribe(prov config.ProviderConfig, vars SubscribeHookVars) error {
	if strings.TrimSpace(prov.Subscribe) == "" {
		return fmt.Errorf("provider %q declares no subscribe hook", prov.ID)
	}
	cmdStr, err := renderSubscribeHook(prov.Subscribe, vars)
	if err != nil {
		return fmt.Errorf("provider %q subscribe template: %w", prov.ID, err)
	}
	_, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return fmt.Errorf("provider %q subscribe: %w: %s", prov.ID, runErr, msg)
		}
		return fmt.Errorf("provider %q subscribe: %w", prov.ID, runErr)
	}
	return nil
}

// renderSubscribeHook renders a subscribe hook template. Strict
// (missingkey=error) like setup: the surface is a fixed struct so a missing
// key is an author typo, not a runtime-empty value.
func renderSubscribeHook(cmd string, vars SubscribeHookVars) (string, error) {
	tmpl, err := template.New("subscribe_hook").
		Option("missingkey=error").
		Funcs(templateFuncs).
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
