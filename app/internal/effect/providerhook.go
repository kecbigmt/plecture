package effect

import (
	"context"
	"os"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// WorkflowHookVars is the context workflow-level setup/cleanup resolves
// against.
// Deliberately minimal: setup runs before the workspace (and thus the full
// config cascade) exists, so it only gets the resource identifier, the
// session name, the configured workspace-dirs root, and the frozen session
// inputs. Anything else the script needs (URL parsing etc.) it derives
// itself — resolver captures are NOT forwarded, so the resolver's regex
// never becomes setup's input contract. Plugins is the one exception: plugin
// mounting resolves from global config independently of any workspace, so it
// is already available at this point in the lifecycle, and a workspace
// provider hook needs it to invoke its own plugin's executables through
// `bin`.
type WorkflowHookVars struct {
	ResourceID        string
	SessionName       string
	WorkspaceDirsRoot string
	SessionInputs     map[string]any
	// Inputs are the workspace provider's author-declared parameters, already
	// validated against its `[inputs_schema]`. Unlike SessionInputs (per-run
	// values a caller passes to `plect up`) these are the workflow's static
	// wiring of the provider itself. The `subscribe` hook does not receive
	// them: it resolves a provider from the resource alone, with no workflow
	// in scope to have set them.
	Inputs  map[string]any
	Plugins []plugins.Mounted
	// SourcePath is the workspace provider definition's own file path
	// (config.WorkspaceProviderConfig.SourcePath), threaded through so a
	// `bin = "<name>"` in Setup/Cleanup can resolve against the workspace
	// provider's containing plugin.
	SourcePath string
	// Force mirrors the caller's --force intent into cleanup's `force` root so
	// a workspace provider's cleanup script can decide for itself whether to
	// force-remove a dirty workspace; core has no opinion on what a
	// workspace provider's release step does with it. Setup never sets
	// this — force only applies to teardown.
	Force bool
	// CleanupInputs are opaque key/value pairs the caller passes through to
	// cleanup's `cleanup.inputs.*` root, unexamined by core. This is the
	// generic escape hatch for workspace-provider-specific teardown intents,
	// so a new one never requires a core vocabulary addition. Setup never
	// sets this — cleanup intents only apply to teardown.
	CleanupInputs map[string]string
}

// ProviderRoots builds the roots one provider hook observes. self and
// cleanup-only roots are absent for setup, which is what keeps a setup
// action from projecting an output it is itself producing.
func ProviderRoots(vars WorkflowHookVars, prev, self map[string]any, cleanup bool) lang.Roots {
	env := lang.Roots{
		"resource": map[string]any{"id": vars.ResourceID},
		"session": map[string]any{
			"name":   vars.SessionName,
			"inputs": normalizeOutputs(vars.SessionInputs),
		},
		"inputs": normalizeOutputs(vars.Inputs),
		"config": map[string]any{"workspace_dirs_root": vars.WorkspaceDirsRoot},
	}
	if cleanup {
		env["self"] = map[string]any{"outputs": normalizeOutputs(self)}
		env["cleanup"] = map[string]any{"inputs": stringMapAsAny(vars.CleanupInputs)}
		env["force"] = vars.Force
		return env
	}
	env["prev"] = normalizeOutputs(prev)
	return env
}

// ProviderEval resolves a provider hook's values, with `bin` resolving
// against the plugin that declared it.
func ProviderEval(env lang.Roots, mounted []plugins.Mounted, sourcePath string, from lang.Ownership) lang.Eval {
	bins := config.MountedBins{Mounted: mounted, SourcePath: sourcePath}
	return lang.Eval{
		Roots: env,
		Bin:   func(ref string) (string, error) { return bins.ResolveBin(ref, from) },
	}
}

// RunProviderAction resolves one provider hook and runs it on the host. A
// shell action gets a private run directory for the binding transport,
// created only for that variant.
func RunProviderAction(action *lang.Action, eval lang.Eval) (stdout, stderr []byte, err error) {
	runDir := ""
	if action.Type == lang.ActionShell {
		dir, mkErr := os.MkdirTemp("", "plect-provider-")
		if mkErr != nil {
			return nil, nil, mkErr
		}
		defer os.RemoveAll(dir)
		runDir = dir
	}
	execution, err := eval.Run(runDir, action, nil)
	if err != nil {
		return nil, nil, err
	}
	// No workspace exists yet by definition for setup, and cleanup may be
	// releasing the one it had, so a provider hook runs from the caller's cwd
	// and must use absolute paths.
	return RunHook(context.Background(), execution, "")
}

// stringMapAsAny widens CleanupInputs so a projection of a cleanup intent the
// caller never expressed resolves through the value's own default rather than
// failing on the map's type.
func stringMapAsAny(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// normalizeNumbers converts integer-valued float64 entries to int64. JSON
// unmarshal into map[string]any leaves every number as float64; templates
// render large float64 as scientific notation (e.g. 3.052179e+06), which
// breaks scripts that compare the rendered value as a string (`pid` etc.).
// Walks nested maps and slices so deep outputs are covered too.
//
// Duplicated from app/internal/task rather than shared: it is a small pure
// helper and the two packages must not import each other.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeNumbers(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeNumbers(vv)
		}
		return out
	}
	return v
}

func normalizeOutputs(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out, _ := normalizeNumbers(m).(map[string]any)
	return out
}
