package task

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	contract "github.com/kecbigmt/plecture/contracts/state"
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

// workflowHookScope is the Observer scope label for pseudo-node events.
const workflowHookScope = "workflow"

// providerRoots builds the roots one provider hook observes. self and
// cleanup-only roots are absent for setup, which is what keeps a setup
// action from projecting an output it is itself producing.
func providerRoots(vars WorkflowHookVars, prev, self map[string]any, cleanup bool) lang.Roots {
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

// providerEval resolves a provider hook's values, with `bin` resolving
// against the plugin that declared it.
func providerEval(env lang.Roots, mounted []plugins.Mounted, sourcePath string, from lang.Ownership) lang.Eval {
	bins := config.MountedBins{Mounted: mounted, SourcePath: sourcePath}
	return lang.Eval{
		Roots: env,
		Bin:   func(ref string) (string, error) { return bins.ResolveBin(ref, from) },
	}
}

// runProviderAction resolves one provider hook and runs it on the host. A
// shell action gets a private run directory for the binding transport,
// created only for that variant.
func runProviderAction(action *lang.Action, eval lang.Eval) (stdout, stderr []byte, err error) {
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
	return runHook(context.Background(), execution, "")
}

// RunWorkflowSetup executes the workspace provider setup hook (the
// workflow-level lifecycle) and persists the result as the @workflow
// pseudo-node in tasks. Semantics mirror RunSetup: idempotent (an
// already-produced pseudo-node is skipped), `prev.*` carries the prior outputs
// across retries, stdout is the JSON outputs contract.
//
// Additional contract: the outputs MUST contain the reserved `workspace_dir`
// key (non-empty string) — every downstream consumer (cascade resolution,
// task cwd, cd/attach) depends on it. The outputs are validated against the
// workflow's outputs schema when one is declared.
//
// Returns the pseudo-node outputs (whether fresh or reused).
func RunWorkflowSetup(prov config.WorkspaceProviderConfig, vars WorkflowHookVars, tasks map[string]*contract.TaskState, observer Observer) (map[string]any, error) {
	obs := observerOr(observer)
	id := contract.WorkflowPseudoNodeID

	if existing, ok := tasks[id]; ok && existing != nil && existing.Status == contract.TaskStatusProduced {
		obs.OnSkip(workflowHookScope, id, "already produced")
		return existing.Outputs, nil
	}
	if prov.Setup == nil {
		return nil, fmt.Errorf("workspace provider %q declares no setup", prov.ID)
	}

	obs.OnStart(workflowHookScope, id)
	now := time.Now()
	var prev map[string]any
	if existing, ok := tasks[id]; ok && existing != nil {
		prev = existing.Outputs
	}

	fail := func(errMsg string) {
		tasks[id] = &contract.TaskState{
			Scope:    contract.TaskScopeSession,
			Status:   contract.TaskStatusFailed,
			Outputs:  prev,
			FailedAt: now,
			Error:    errMsg,
		}
	}

	eval := providerEval(providerRoots(vars, prev, nil, false), vars.Plugins, vars.SourcePath, prov.Ownership())
	stdout, stderr, runErr := runProviderAction(prov.Setup, eval)
	if runErr != nil {
		fail(runErr.Error())
		wrapped := fmt.Errorf("workspace provider %q setup: %w", prov.ID, runErr)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	outputs, parseErr := ParseOutputs(stdout)
	if parseErr != nil {
		fail(parseErr.Error())
		wrapped := fmt.Errorf("workspace provider %q setup: %w", prov.ID, parseErr)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	workspaceDir, _ := outputs[contract.OutputKeyWorkspaceDir].(string)
	if strings.TrimSpace(workspaceDir) == "" {
		msg := fmt.Sprintf("setup outputs must contain a non-empty %q string (got %v)", contract.OutputKeyWorkspaceDir, outputs[contract.OutputKeyWorkspaceDir])
		fail(msg)
		wrapped := fmt.Errorf("workspace provider %q setup: %s", prov.ID, msg)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	schema, err := CompileSchema(prov.OutputsSchema, prov.ResolvedOutputsSchemaPath(), "plect:workspace_provider:"+prov.ID+":outputs")
	if err != nil {
		fail(err.Error())
		wrapped := fmt.Errorf("workspace provider %q outputs schema: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	if schema != nil {
		if vErr := schema.Validate(outputs); vErr != nil {
			fail(vErr.Error())
			wrapped := fmt.Errorf("workspace provider %q setup: outputs schema: %w", prov.ID, vErr)
			obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
			return nil, wrapped
		}
	}

	tasks[id] = &contract.TaskState{
		Scope:   contract.TaskScopeSession,
		Status:  contract.TaskStatusProduced,
		Outputs: outputs,
		Seq:     nextSeq(tasks),
		SetupAt: now,
	}
	obs.OnSuccess(workflowHookScope, id, time.Since(now), stderr)
	return outputs, nil
}

// RunWorkflowCleanup executes the workflow-level cleanup hook. Mirrors
// RunCleanup semantics: missing/cleaned state is skipped, an empty cleanup
// body flips the state to cleaned, errors mark the pseudo-node failed and
// are returned (callers decide fail-fast vs --force).
//
// The hook intentionally never deletes workspace_dir itself — setup/cleanup
// symmetry is the script author's contract ("use an existing directory"
// workflows must stay possible). Outputs survive cleanup (same invariant as
// task cleanup) so a later setup retry can read `prev.*`.
func RunWorkflowCleanup(prov config.WorkspaceProviderConfig, vars WorkflowHookVars, tasks map[string]*contract.TaskState, observer Observer) error {
	obs := observerOr(observer)
	id := contract.WorkflowPseudoNodeID

	state, ok := tasks[id]
	if !ok || state == nil {
		obs.OnSkip(workflowHookScope, id, "no setup state")
		return nil
	}
	if state.Status == contract.TaskStatusCleaned {
		obs.OnSkip(workflowHookScope, id, "already cleaned")
		return nil
	}
	now := time.Now()
	if prov.Cleanup == nil {
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		obs.OnSuccess(workflowHookScope, id, time.Since(now), nil)
		return nil
	}

	obs.OnStart(workflowHookScope, id)
	eval := providerEval(providerRoots(vars, nil, state.Outputs, true), vars.Plugins, vars.SourcePath, prov.Ownership())
	_, stderr, runErr := runProviderAction(prov.Cleanup, eval)
	if runErr != nil {
		state.Status = contract.TaskStatusFailed
		state.Error = runErr.Error()
		state.FailedAt = now
		wrapped := fmt.Errorf("workspace provider %q cleanup: %w", prov.ID, runErr)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return wrapped
	}
	state.Status = contract.TaskStatusCleaned
	state.CleanedAt = now
	state.Error = ""
	obs.OnSuccess(workflowHookScope, id, time.Since(now), stderr)
	return nil
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
