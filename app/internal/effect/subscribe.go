package effect

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// SubscribeHookVars carries the concrete resource and session into a delivery hook.
type SubscribeHookVars struct {
	ResourceID  string
	Inputs      map[string]any
	SessionName string
	Plugins     []plugins.Mounted
	SourcePath  string
}

func RunResourceSubscribe(def config.ResourceDef, vars SubscribeHookVars) error {
	return runResourceDeliveryHook(def, def.Subscribe, "subscribe", vars)
}

func RunResourceUnsubscribe(def config.ResourceDef, vars SubscribeHookVars) error {
	return runResourceDeliveryHook(def, def.Unsubscribe, "unsubscribe", vars)
}

func runResourceDeliveryHook(def config.ResourceDef, action *lang.Action, name string, vars SubscribeHookVars) error {
	if action == nil {
		return fmt.Errorf("resource %q declares no %s hook", def.ID, name)
	}
	inputs := vars.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	env := lang.Roots{
		"session":  map[string]any{"name": vars.SessionName},
		"resource": map[string]any{"id": vars.ResourceID, "inputs": inputs},
	}
	_, stderr, runErr := RunHookAction(action, HookEval(env, vars.Plugins, vars.SourcePath, def.Ownership()))
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return fmt.Errorf("resource %q %s: %w: %s", def.ID, name, runErr, msg)
		}
		return fmt.Errorf("resource %q %s: %w", def.ID, name, runErr)
	}
	return nil
}
