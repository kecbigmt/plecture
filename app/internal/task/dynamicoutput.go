package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// FetchOutput runs the script (or, when FromResourceStatus is set, observes
// the instance's bound resource) and returns the values keyed by output name.
// A produced key the JSON/state omits stays unset, and any failure returns an
// error — both leave a check pending, not unsatisfied.
func FetchOutput(goCtx context.Context, cfg *config.Config, src config.DynamicOutput, ctx RenderContext) (map[string]string, error) {
	if src.FromResourceStatus {
		return fetchFromResourceStatus(cfg, src, ctx)
	}
	cmdStr, rerr := render(src.Script, ctx)
	if rerr != nil {
		return nil, fmt.Errorf("output %v: script template: %w", src.OutputNames(), rerr)
	}
	stdout, stderr, runErr := execHostScript(goCtx, cmdStr, ctx.Session.WorkdirPath)
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("output %v: %w: %s", src.OutputNames(), runErr, msg)
		}
		return nil, fmt.Errorf("output %v: %w", src.OutputNames(), runErr)
	}
	out := strings.TrimSpace(string(stdout))

	if src.Name != "" {
		return map[string]string{src.Name: out}, nil
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		return nil, fmt.Errorf("output %v: script stdout is not a JSON object: %w", src.Produces, err)
	}
	values := map[string]string{}
	for _, name := range src.Produces {
		if v, ok := obj[name]; ok && v != nil {
			values[name] = valueString(v)
		}
	}
	return values, nil
}

// fetchFromResourceStatus is the from_resource_status path: it resolves the
// instance's bound resource against the declared resource definitions
// (ADR "goal-as-task" D1/D2 resolution face) and copies the requested keys
// from its observed state, instead of running a script of its own.
func fetchFromResourceStatus(cfg *config.Config, src config.DynamicOutput, ctx RenderContext) (map[string]string, error) {
	resourceID := strings.TrimSpace(ctx.Session.ResourceID)
	if resourceID == "" {
		return nil, fmt.Errorf("output %v: from_resource_status requires the instance to have a bound resource (--resource)", src.OutputNames())
	}
	defs, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, fmt.Errorf("output %v: load resource definitions: %w", src.OutputNames(), err)
	}
	state, _, ok, err := ResourceStatus(defs, resourceID, ctx.Session.Branch, ctx.Session.WorkdirPath)
	if err != nil {
		return nil, fmt.Errorf("output %v: %w", src.OutputNames(), err)
	}
	if !ok {
		return nil, fmt.Errorf("output %v: no resource definition recognizes %q", src.OutputNames(), resourceID)
	}
	values := map[string]string{}
	for _, name := range src.Produces {
		if v, ok := state[name]; ok && v != nil {
			values[name] = valueString(v)
		}
	}
	return values, nil
}

func valueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
