package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// EnvironmentHookVars is the template surface for environment setup/cleanup.
// Unlike WorkflowHookVars, the workspace already exists by the time
// environment setup runs (it runs after workspace provider setup) — so it is
// available here, along with the workflow's environment_inputs passthrough
// table.
type EnvironmentHookVars struct {
	ResourceID        string
	SessionName       string
	SessionInputs     map[string]any
	WorkspaceDirPath  string
	EnvironmentInputs map[string]any
}

// environmentHookScope is the Observer scope label for pseudo-node events.
const environmentHookScope = "environment"

// RunEnvironmentSetup executes the environment's setup hook and persists the
// result as the @environment pseudo-node in tasks. Semantics mirror
// RunWorkflowSetup: idempotent (an already-produced pseudo-node is skipped),
// .Prev carries prior outputs across retries. Unlike a workspace provider's
// setup, EnvironmentConfig.Setup is optional — an environment with nothing to
// acquire still produces an empty-outputs pseudo-node so downstream
// Execution="environment" nodes and .Environment.outputs render consistently.
//
// Returns the pseudo-node outputs (whether fresh or reused).
func RunEnvironmentSetup(env config.EnvironmentConfig, vars EnvironmentHookVars, tasks map[string]*contract.TaskState, observer Observer) (map[string]any, error) {
	obs := observerOr(observer)
	id := contract.EnvironmentPseudoNodeID

	if existing, ok := tasks[id]; ok && existing != nil && existing.Status == contract.TaskStatusProduced {
		obs.OnSkip(environmentHookScope, id, "already produced")
		return existing.Outputs, nil
	}

	obs.OnStart(environmentHookScope, id)
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

	outputs := map[string]any{}
	var stderr []byte
	if strings.TrimSpace(env.Setup) != "" {
		cmdStr, err := renderEnvironmentHook(env.Setup, vars, prev, nil, "missingkey=error")
		if err != nil {
			fail(err.Error())
			wrapped := fmt.Errorf("environment %q setup template: %w", env.ID, err)
			obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, nil)
			return nil, wrapped
		}
		stdout, capturedStderr, runErr := runShell(cmdStr, vars.WorkspaceDirPath)
		stderr = capturedStderr
		if runErr != nil {
			fail(runErr.Error())
			wrapped := fmt.Errorf("environment %q setup: %w", env.ID, runErr)
			obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, stderr)
			return nil, wrapped
		}
		parsed, parseErr := ParseOutputs(stdout)
		if parseErr != nil {
			fail(parseErr.Error())
			wrapped := fmt.Errorf("environment %q setup: %w", env.ID, parseErr)
			obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, stderr)
			return nil, wrapped
		}
		outputs = parsed
	}
	schema, err := CompileSchema(env.OutputsSchema, env.ResolvedOutputsSchemaPath(), "plect:environment:"+env.ID+":outputs")
	if err != nil {
		fail(err.Error())
		wrapped := fmt.Errorf("environment %q outputs schema: %w", env.ID, err)
		obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, stderr)
		return nil, wrapped
	}
	if schema != nil {
		if vErr := schema.Validate(outputs); vErr != nil {
			fail(vErr.Error())
			wrapped := fmt.Errorf("environment %q setup: outputs schema: %w", env.ID, vErr)
			obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, stderr)
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
	obs.OnSuccess(environmentHookScope, id, time.Since(now), stderr)
	return outputs, nil
}

// RunEnvironmentCleanup executes the environment's cleanup hook. Mirrors
// RunWorkflowCleanup semantics: missing/cleaned state is skipped, an empty
// cleanup body flips the state to cleaned, errors mark the pseudo-node
// failed and are returned (callers decide fail-fast vs --force).
func RunEnvironmentCleanup(env config.EnvironmentConfig, vars EnvironmentHookVars, tasks map[string]*contract.TaskState, observer Observer) error {
	obs := observerOr(observer)
	id := contract.EnvironmentPseudoNodeID

	state, ok := tasks[id]
	if !ok || state == nil {
		obs.OnSkip(environmentHookScope, id, "no setup state")
		return nil
	}
	if state.Status == contract.TaskStatusCleaned {
		obs.OnSkip(environmentHookScope, id, "already cleaned")
		return nil
	}
	now := time.Now()
	if strings.TrimSpace(env.Cleanup) == "" {
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		obs.OnSuccess(environmentHookScope, id, time.Since(now), nil)
		return nil
	}

	obs.OnStart(environmentHookScope, id)
	cmdStr, err := renderEnvironmentHook(env.Cleanup, vars, nil, state.Outputs, "missingkey=zero")
	if err != nil {
		state.Status = contract.TaskStatusFailed
		state.Error = err.Error()
		state.FailedAt = now
		wrapped := fmt.Errorf("environment %q cleanup template: %w", env.ID, err)
		obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, nil)
		return wrapped
	}
	_, stderr, runErr := runShell(cmdStr, vars.WorkspaceDirPath)
	if runErr != nil {
		state.Status = contract.TaskStatusFailed
		state.Error = runErr.Error()
		state.FailedAt = now
		wrapped := fmt.Errorf("environment %q cleanup: %w", env.ID, runErr)
		obs.OnFailure(environmentHookScope, id, time.Since(now), wrapped, stderr)
		return wrapped
	}
	state.Status = contract.TaskStatusCleaned
	state.CleanedAt = now
	state.Error = ""
	obs.OnSuccess(environmentHookScope, id, time.Since(now), stderr)
	return nil
}

// renderEnvironmentHook renders an environment hook template. Cleanup
// additionally sees .Self (the persisted setup outputs); setup sees .Prev
// (prior outputs, for idempotent retry). Mirrors renderWorkflowHook.
func renderEnvironmentHook(cmd string, vars EnvironmentHookVars, prev, self map[string]any, opt string) (string, error) {
	tmpl, err := template.New("environment_hook").
		Option(opt).
		Funcs(templateFuncs).
		Parse(cmd)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	data := struct {
		ResourceID        string
		SessionName       string
		SessionInputs     map[string]any
		WorkspaceDirPath  string
		EnvironmentInputs map[string]any
		Prev              map[string]any
		Self              map[string]any
	}{
		ResourceID:        vars.ResourceID,
		SessionName:       vars.SessionName,
		SessionInputs:     normalizeOutputs(vars.SessionInputs),
		WorkspaceDirPath:  vars.WorkspaceDirPath,
		EnvironmentInputs: normalizeOutputs(vars.EnvironmentInputs),
		Prev:              normalizeOutputs(prev),
		Self:              normalizeOutputs(self),
	}
	if data.SessionInputs == nil {
		data.SessionInputs = map[string]any{}
	}
	if data.EnvironmentInputs == nil {
		data.EnvironmentInputs = map[string]any{}
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
