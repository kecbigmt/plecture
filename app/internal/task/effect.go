package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// The environments below are docs/language/values.md's per-surface root
// table, one constructor per effect surface. A surface exposes only the
// context it is allowed to observe, so a root a surface does not offer is
// absent from the tree rather than present and ignored — which is also what
// lets a value's own `default` fire instead of reading an empty string.

func setupEnvironment(ctx RenderContext) lang.Environment {
	env := sessionRoots(ctx)
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	env["prev"] = normalizeOutputs(ctx.Prev)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	if ctx.Session.ResourceID != "" {
		env["resource"] = map[string]any{"id": ctx.Session.ResourceID}
	}
	return env
}

func cleanupEnvironment(ctx RenderContext) lang.Environment {
	env := sessionRoots(ctx)
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Self))}
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	return env
}

func healthEnvironment(ctx RenderContext) lang.Environment {
	env := sessionRoots(ctx)
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Self))}
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	return env
}

func terminalEnvironment(self map[string]any, session SessionVars) lang.Environment {
	env := sessionRoots(RenderContext{Session: session})
	delete(env, "workspace")
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(self))}
	return env
}

func innerEnvironment(ctx RenderContext) lang.Environment {
	env := sessionRoots(ctx)
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	env["locals"] = normalizeOutputs(ctx.Locals)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	return env
}

// outputsBindEnvironment observes no live root and no session: an effect's
// outputs are production records, fixed when the layer is instantiated.
func outputsBindEnvironment(ctx RenderContext) lang.Environment {
	return lang.Environment{
		"inner":  map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Inner))},
		"locals": normalizeOutputs(ctx.Locals),
		"inputs": normalizeOutputs(ctx.Inputs),
	}
}

func sessionRoots(ctx RenderContext) lang.Environment {
	session := map[string]any{"name": ctx.Session.Name}
	if ctx.Session.ParentSession != "" {
		session["parent"] = ctx.Session.ParentSession
	}
	if inputs := normalizeOutputs(ctx.Session.Inputs); len(inputs) > 0 {
		session["inputs"] = inputs
	}
	env := lang.Environment{"session": session}
	if workspace := presentStrings(map[string]string{
		"dir":    ctx.Session.WorkspaceDirPath,
		"branch": ctx.Session.Branch,
	}); workspace != nil {
		env["workspace"] = workspace
	}
	return env
}

func presentStrings(in map[string]string) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func nodeRoots(tasks map[string]map[string]any) map[string]any {
	if len(tasks) == 0 {
		return nil
	}
	out := make(map[string]any, len(tasks))
	for id, outputs := range tasks {
		out[id] = map[string]any{"outputs": normalizeOutputs(outputs)}
	}
	return out
}

// effectEval pairs one surface's environment with this machine's two
// capabilities: an executable's path and the plan's interactive endpoint.
// dir is the private run directory a materialized terminal verb is written
// into, so a resolved verb lives exactly as long as the execution consuming
// it.
func effectEval(env lang.Environment, ctx RenderContext, from lang.Ownership, dir string) lang.Eval {
	bins := config.MountedBins{Mounted: ctx.Session.Plugins, SourcePath: ctx.SourcePath}
	eval := lang.Eval{
		Env: env,
		Bin: func(ref string) (string, error) { return bins.ResolveBin(ref, from) },
	}
	if binding := ctx.Session.Terminal; binding != nil {
		eval.Terminal = func(verb string) (string, error) {
			return TerminalCommand(binding, verb, ctx.Session, dir)
		}
	}
	return eval
}

// TerminalCommand resolves one terminal verb into the command string a
// consuming script runs as `sh -c "$verb" <name> <operand>`: the verb's own
// operand arrives as the shell's first positional parameter, whichever
// action variant declared it.
//
// A shell verb is materialized into dir, so the caller's control of dir's
// lifetime is what keeps the resolved verb runnable for exactly as long as
// whatever consumes it.
func TerminalCommand(binding *TerminalBinding, verb string, session SessionVars, dir string) (string, error) {
	if binding == nil {
		return "", fmt.Errorf("terminal %q: no effect in this workflow's plan declares an interactive endpoint", verb)
	}
	action, err := binding.Ops.Verb(verb)
	if err != nil {
		return "", err
	}
	eval := lang.Eval{
		Env: terminalEnvironment(binding.Outputs, session),
		Bin: func(ref string) (string, error) {
			bins := config.MountedBins{Mounted: session.Plugins, SourcePath: binding.SourcePath}
			return bins.ResolveBin(ref, binding.From)
		},
	}
	execution, err := eval.Run(filepath.Join(dir, "terminal-"+verb), action, nil)
	if err != nil {
		return "", err
	}
	words := make([]string, 0, len(execution.Argv)+1)
	for _, arg := range execution.Argv {
		words = append(words, shellWord(arg))
	}
	return strings.Join(words, " ") + ` "$@"`, nil
}

// shellWord quotes one word so the shell reads every byte of it literally.
func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// effectExecution is one resolved action: the process it runs, plus the
// private run directory that process depends on — a shell action's binding
// file and any terminal verb the action consumes live there, so the
// directory has to outlive the resolution and not the process.
type effectExecution struct {
	execution *lang.Execution
	dir       string
}

// resolveEffect resolves one effect action against its surface. Resolution
// is separate from running so a value that cannot be resolved is reported as
// the configuration error it is rather than as a failed execution.
func resolveEffect(action *lang.Action, env lang.Environment, ctx RenderContext, from lang.Ownership, operands []string) (*effectExecution, error) {
	dir, err := os.MkdirTemp("", "plect-effect-")
	if err != nil {
		return nil, err
	}
	execution, err := effectEval(env, ctx, from, dir).Run(filepath.Join(dir, "action"), action, operands)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &effectExecution{execution: execution, dir: dir}, nil
}

// run executes the resolved process through the swappable executor every
// in-session execution takes.
func (e *effectExecution) run(goCtx context.Context, workDir string, processEnv ...string) (stdout, stderr []byte, err error) {
	return execHook(goCtx, e.execution, workDir, processEnv...)
}

func (e *effectExecution) close() {
	if e != nil {
		os.RemoveAll(e.dir)
	}
}

// resolveValues resolves one value table — a nesting joint's inputs or
// environment — into the strings the next layer inward receives. Keys are
// walked in order so a diagnostic and a recorded execution are reproducible
// rather than map-ordered.
func resolveValues(values map[string]*lang.Value, env lang.Environment, ctx RenderContext, from lang.Ownership) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	// A joint value reaches no terminal verb, so the run directory an
	// effectEval otherwise materializes one into is never written to.
	eval := effectEval(env, ctx, from, "")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(values))
	for _, key := range keys {
		resolved, absent, err := eval.Argument(values[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if absent {
			continue
		}
		out[key] = resolved
	}
	return out, nil
}
