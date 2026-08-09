package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// WorkflowHookVars is the template surface for workflow-level setup/cleanup.
// Deliberately minimal: setup runs before the working directory (and thus the
// full config cascade) exists, so it only gets the resource identifier, the
// session name, and the frozen session inputs. Anything else the script needs
// (URL parsing etc.) it derives itself — resolver captures are NOT forwarded,
// so the resolver's regex never becomes setup's input contract.
type WorkflowHookVars struct {
	ResourceID    string
	SessionName   string
	SessionInputs map[string]any
}

// workflowHookScope is the Observer scope label for pseudo-node events.
const workflowHookScope = "workflow"

// RunWorkflowSetup executes the provider setup hook (the workflow-level
// lifecycle) and persists the
// result as the @workflow pseudo-node in tasks. Semantics mirror RunSetup:
// idempotent (an already-produced pseudo-node is skipped), .Prev carries the
// prior outputs across retries, stdout is the JSON outputs contract.
//
// Additional contract: the outputs MUST contain the reserved `workdir` key
// (non-empty string) — every downstream consumer (cascade resolution, task
// cwd, cd/attach) depends on it. The outputs are validated against the
// workflow's outputs schema when one is declared.
//
// Returns the pseudo-node outputs (whether fresh or reused).
func RunWorkflowSetup(prov config.ProviderConfig, vars WorkflowHookVars, tasks map[string]*contract.TaskState, observer Observer) (map[string]any, error) {
	obs := observerOr(observer)
	id := contract.WorkflowPseudoNodeID

	if existing, ok := tasks[id]; ok && existing != nil && existing.Status == contract.TaskStatusProduced {
		obs.OnSkip(workflowHookScope, id, "already produced")
		return existing.Outputs, nil
	}
	if strings.TrimSpace(prov.Setup) == "" {
		return nil, fmt.Errorf("provider %q declares no setup", prov.ID)
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
		wrapped := fmt.Errorf("provider %q setup template: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, nil)
		return nil, wrapped
	}

	// No workdir exists yet by definition — the script runs from the
	// caller's cwd and must use absolute paths.
	stdout, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		fail(runErr.Error())
		wrapped := fmt.Errorf("provider %q setup: %w", prov.ID, runErr)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	outputs, parseErr := ParseOutputs(stdout)
	if parseErr != nil {
		fail(parseErr.Error())
		wrapped := fmt.Errorf("provider %q setup: %w", prov.ID, parseErr)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	workdir, _ := outputs[contract.OutputKeyWorkdir].(string)
	if strings.TrimSpace(workdir) == "" {
		msg := fmt.Sprintf("setup outputs must contain a non-empty %q string (got %v)", contract.OutputKeyWorkdir, outputs[contract.OutputKeyWorkdir])
		fail(msg)
		wrapped := fmt.Errorf("provider %q setup: %s", prov.ID, msg)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	schema, err := CompileSchema(prov.OutputsSchema, prov.ResolvedOutputsSchemaPath(), "tws:provider:"+prov.ID+":outputs")
	if err != nil {
		fail(err.Error())
		wrapped := fmt.Errorf("provider %q outputs schema: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	if schema != nil {
		if vErr := schema.Validate(outputs); vErr != nil {
			fail(vErr.Error())
			wrapped := fmt.Errorf("provider %q setup: outputs schema: %w", prov.ID, vErr)
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
// The hook intentionally never deletes workdir itself — setup/cleanup
// symmetry is the script author's contract ("use an existing directory"
// workflows must stay possible). Outputs survive cleanup (same invariant as
// task cleanup) so a later setup retry can read .Prev.
func RunWorkflowCleanup(prov config.ProviderConfig, vars WorkflowHookVars, tasks map[string]*contract.TaskState, observer Observer) error {
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
		wrapped := fmt.Errorf("provider %q cleanup template: %w", prov.ID, err)
		obs.OnFailure(workflowHookScope, id, time.Since(now), wrapped, nil)
		return wrapped
	}
	_, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		state.Status = contract.TaskStatusFailed
		state.Error = runErr.Error()
		state.FailedAt = now
		wrapped := fmt.Errorf("provider %q cleanup: %w", prov.ID, runErr)
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
	tmpl, err := template.New("workflow_hook").
		Option(opt).
		Funcs(templateFuncs).
		Parse(cmd)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	data := struct {
		ResourceID    string
		SessionName   string
		SessionInputs map[string]any
		Prev          map[string]any
		Self          map[string]any
	}{
		ResourceID:    vars.ResourceID,
		SessionName:   vars.SessionName,
		SessionInputs: normalizeOutputs(vars.SessionInputs),
		Prev:          normalizeOutputs(prev),
		Self:          normalizeOutputs(self),
	}
	if data.SessionInputs == nil {
		data.SessionInputs = map[string]any{}
	}
	if data.Prev == nil {
		data.Prev = map[string]any{}
	}
	if data.Self == nil {
		data.Self = map[string]any{}
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
