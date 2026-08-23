package task

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// The environments below are docs/language/values.md's per-surface root
// table, one constructor per effect surface. A surface exposes only the
// context it is allowed to observe, so a root a surface does not offer is
// absent from the tree rather than present and ignored — which is also what
// lets a value's own `default` fire instead of reading an empty string.

func setupRoots(ctx RenderContext) lang.Roots {
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

func cleanupRoots(ctx RenderContext) lang.Roots {
	env := sessionRoots(ctx)
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Self))}
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	return env
}

func healthRoots(ctx RenderContext) lang.Roots {
	env := sessionRoots(ctx)
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Self))}
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	return env
}

func terminalRoots(self map[string]any, session SessionVars) lang.Roots {
	env := sessionRoots(RenderContext{Session: session})
	delete(env, "workspace")
	env["self"] = map[string]any{"outputs": orEmpty(normalizeOutputs(self))}
	return env
}

func innerRoots(ctx RenderContext) lang.Roots {
	env := sessionRoots(ctx)
	env["inputs"] = normalizeOutputs(ctx.Inputs)
	env["locals"] = normalizeOutputs(ctx.Locals)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	return env
}

// outputsBindRoots observes no live root and no session: an effect's
// outputs are production records, fixed when the layer is instantiated.
func outputsBindRoots(ctx RenderContext) lang.Roots {
	return lang.Roots{
		"inner":  map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Inner))},
		"locals": normalizeOutputs(ctx.Locals),
		"inputs": normalizeOutputs(ctx.Inputs),
	}
}

func sessionRoots(ctx RenderContext) lang.Roots {
	session := map[string]any{"name": ctx.Session.Name}
	if ctx.Session.ParentSession != "" {
		session["parent"] = ctx.Session.ParentSession
	}
	if inputs := normalizeOutputs(ctx.Session.Inputs); len(inputs) > 0 {
		session["inputs"] = inputs
	}
	env := lang.Roots{"session": session}
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

// capabilitiesFor adapts ctx's session/plugin context into the plain
// closures effect.Resolve needs, so effect never has to know what a
// RenderContext or a TerminalBinding is.
func capabilitiesFor(ctx RenderContext, from lang.Ownership) effect.Capabilities {
	bins := config.MountedBins{Mounted: ctx.Session.Plugins, SourcePath: ctx.SourcePath}
	caps := effect.Capabilities{
		Bin: func(ref string) (string, error) { return bins.ResolveBin(ref, from) },
	}
	if binding := ctx.Session.Terminal; binding != nil {
		caps.Terminal = func(dir, verb string) (string, error) {
			return TerminalCommand(binding, verb, ctx.Session, dir)
		}
	}
	return caps
}

// effectEval pairs one surface's environment with this machine's two
// capabilities: an executable's path and the plan's interactive endpoint.
// dir is the private run directory a materialized terminal verb is written
// into, so a resolved verb lives exactly as long as the execution consuming
// it.
func effectEval(env lang.Roots, ctx RenderContext, from lang.Ownership, dir string) lang.Eval {
	return capabilitiesFor(ctx, from).Eval(env, dir)
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
		Roots: terminalRoots(binding.Outputs, session),
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

// resolveEffect resolves one effect action against its surface. Resolution
// is separate from running so a value that cannot be resolved is reported as
// the configuration error it is rather than as a failed execution.
func resolveEffect(action *lang.Action, env lang.Roots, ctx RenderContext, from lang.Ownership, operands []string) (*effect.Execution, error) {
	return effect.Resolve(action, env, capabilitiesFor(ctx, from), operands)
}

// resolveValues resolves one value table — a nesting joint's inputs or
// environment — into the strings the next layer inward receives.
func resolveValues(values map[string]*lang.Value, env lang.Roots, ctx RenderContext, from lang.Ownership) (map[string]string, error) {
	return effect.ResolveValues(values, env, capabilitiesFor(ctx, from))
}
