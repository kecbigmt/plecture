package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// WorkflowHookVars is the template surface for workflow-level setup/cleanup.
// Deliberately minimal: setup runs before the workspace (and thus the full
// config cascade) exists, so it only gets the resource identifier, the
// session name, the configured workspace-dirs root, and the frozen session
// inputs. Anything else the script needs (URL parsing etc.) it derives
// itself — resolver captures are NOT forwarded, so the resolver's regex
// never becomes setup's input contract. Plugins is the one exception: plugin
// mounting resolves from global config independently of any workspace, so it
// is already available at this point in the lifecycle, and a workspace
// provider hook needs it to invoke its own plugin's executables through
// `{{bin ...}}`.
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
	// `{{bin "<name>"}}` in Setup/Cleanup can resolve against the workspace
	// provider's containing plugin.
	SourcePath string
	// Force mirrors the caller's --force intent into the cleanup template so
	// a workspace provider's cleanup script can decide for itself whether to
	// force-remove a dirty workspace; core has no opinion on what a
	// workspace provider's release step does with it. Setup never sets
	// this — force only applies to teardown.
	Force bool
	// CleanupInputs are opaque key/value pairs the caller passes through to
	// the cleanup template as .CleanupInputs, unexamined by core. This is the
	// generic escape hatch for workspace-provider-specific teardown intents,
	// so a new one never requires a core vocabulary addition. Setup never
	// sets this — cleanup intents only apply to teardown.
	CleanupInputs map[string]string
}

// workflowHookScope is the Observer scope label for pseudo-node events.
const workflowHookScope = "workflow"

// RunWorkflowSetup executes the workspace provider setup hook (the
// workflow-level lifecycle) and persists the result as the @workflow
// pseudo-node in tasks. Semantics mirror RunSetup: idempotent (an
// already-produced pseudo-node is skipped), .Prev carries the prior outputs
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
	if strings.TrimSpace(prov.Setup) == "" {
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

	cmdStr, err := renderWorkflowHook(prov.Setup, vars, prev, nil, "missingkey=error")
	if err != nil {
		fail(err.Error())
		wrapped := fmt.Errorf("workspace provider %q setup template: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, nil)
		return nil, wrapped
	}

	// No workspace exists yet by definition — the script runs from the
	// caller's cwd and must use absolute paths.
	stdout, stderr, runErr := runShell(cmdStr, "")
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
// task cleanup) so a later setup retry can read .Prev.
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
	if strings.TrimSpace(prov.Cleanup) == "" {
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		obs.OnSuccess(workflowHookScope, id, time.Since(now), nil)
		return nil
	}

	obs.OnStart(workflowHookScope, id)
	cmdStr, err := renderWorkflowHook(prov.Cleanup, vars, nil, state.Outputs, "missingkey=zero")
	if err != nil {
		state.Status = contract.TaskStatusFailed
		state.Error = err.Error()
		state.FailedAt = now
		wrapped := fmt.Errorf("workspace provider %q cleanup template: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, nil)
		return wrapped
	}
	_, stderr, runErr := runShell(cmdStr, "")
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

// renderWorkflowHook renders a workflow hook template. Cleanup additionally
// sees .Self (the persisted setup outputs); setup sees .Prev (prior outputs,
// for idempotent retry). Like renderCleanup, "<no value>" is stripped under
// missingkey=zero so nil never leaks shell metacharacters.
func renderWorkflowHook(cmd string, vars WorkflowHookVars, prev, self map[string]any, opt string) (string, error) {
	// bin is built per render call, not part of the static templateFuncs map,
	// because it resolves against this render's own vars.Plugins — mirrors
	// renderWith's dynamicFuncs for the same reason.
	dynamicFuncs := template.FuncMap{
		"bin": func(ref string) (string, error) {
			return plugins.ResolveBin(vars.Plugins, vars.SourcePath, ref)
		},
	}
	tmpl, err := template.New("workflow_hook").
		Option(opt).
		Funcs(templateFuncs).
		Funcs(dynamicFuncs).
		Parse(cmd)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	data := struct {
		ResourceID        string
		SessionName       string
		WorkspaceDirsRoot string
		SessionInputs     map[string]any
		Inputs            map[string]any
		Prev              map[string]any
		Self              map[string]any
		Force             bool
		CleanupInputs     map[string]any
	}{
		ResourceID:        vars.ResourceID,
		SessionName:       vars.SessionName,
		WorkspaceDirsRoot: vars.WorkspaceDirsRoot,
		SessionInputs:     normalizeOutputs(vars.SessionInputs),
		Inputs:            normalizeOutputs(vars.Inputs),
		Prev:              normalizeOutputs(prev),
		Self:              normalizeOutputs(self),
		Force:             vars.Force,
		CleanupInputs:     stringMapAsAny(vars.CleanupInputs),
	}
	if data.SessionInputs == nil {
		data.SessionInputs = map[string]any{}
	}
	if data.Inputs == nil {
		data.Inputs = map[string]any{}
	}
	if data.Prev == nil {
		data.Prev = map[string]any{}
	}
	if data.Self == nil {
		data.Self = map[string]any{}
	}
	if data.CleanupInputs == nil {
		data.CleanupInputs = map[string]any{}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	out := buf.String()
	if opt == "missingkey=zero" {
		out = strings.ReplaceAll(out, "<no value>", "")
	}
	return out, nil
}

// stringMapAsAny widens CleanupInputs for the render data so the `get` helper
// — which reads map[string]any — reaches a cleanup intent the caller may not
// have expressed at all.
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
