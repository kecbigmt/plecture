package task

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ResolvedLayer is one layer of a nested task after compilation. The layers of
// a node are homogeneous — each is a task's own setup/cleanup pair plus the
// joint wiring it declares toward the layer it wraps — so the runner walks
// them without knowing which one came from a plugin and which from user
// config.
type ResolvedLayer struct {
	TaskID     string
	Setup      *lang.Action
	Cleanup    *lang.Action
	SourcePath string
	From       lang.Ownership
	// InnerInputs / InnerEnv are this layer's joint toward the next layer
	// inward: the values producing that layer's input object, and the process
	// environment added to every execution inside this layer.
	InnerInputs map[string]*lang.Value
	InnerEnv    map[string]*lang.Value
	// BindOutputs is this layer's joint outward: the classified
	// `[outputs.bind]` entries that project the layer inside it into this
	// layer's own public contract.
	BindOutputs []config.OutputBinding
	// InputsSchema validates the inputs this layer is set up with, and
	// LocalsSchema the intermediates its setup emits. OutputsSchema is set
	// only for the innermost layer, whose setup emits the chain's actual
	// task outputs rather than locals.
	InputsSchema  *jsonschema.Schema
	LocalsSchema  *jsonschema.Schema
	OutputsSchema *jsonschema.Schema
	// Health and Terminal are what this layer declares for itself. They
	// compose across the chain rather than override: alive by AND, activity
	// by OR, and at most one [terminal] per chain.
	Health   *config.HealthConfig
	Terminal *config.TerminalConfig
}

// ResolveLayers compiles the schemas and output bindings of every layer of
// def's nesting chain, outermost first. Returns nil for a plain task.
func ResolveLayers(def config.TaskDefinition) ([]ResolvedLayer, error) {
	if !def.IsNested() {
		return nil, nil
	}
	defs := append([]config.TaskDefinition{def}, def.InnerChain...)
	out := make([]ResolvedLayer, 0, len(defs))
	for i, d := range defs {
		layer := ResolvedLayer{
			TaskID:      d.ID,
			Setup:       d.Setup,
			Cleanup:     d.Cleanup,
			SourcePath:  d.SourcePath,
			From:        d.Ownership(),
			InnerInputs: d.InnerInputs,
			InnerEnv:    d.InnerEnv,
			Health:      d.Health,
		}
		if d.Terminal.IsDeclared() {
			layer.Terminal = d.Terminal
		}
		layer.BindOutputs = d.ClassifiedOutputBindings()
		var err error
		if layer.InputsSchema, err = lang.CompileSchema(d.InputsSchema, d.ResolvedInputsSchemaPath(), "plect:task:"+d.ID+":inputs"); err != nil {
			return nil, fmt.Errorf("layer %q: input schema: %w", d.ID, err)
		}
		if i == len(defs)-1 {
			if layer.OutputsSchema, err = lang.CompileSchema(d.OutputsSchema, d.ResolvedOutputsSchemaPath(), "plect:task:"+d.ID+":outputs"); err != nil {
				return nil, fmt.Errorf("layer %q: outputs schema: %w", d.ID, err)
			}
		} else if layer.LocalsSchema, err = lang.CompileSchema(d.LocalsSchema, d.ResolvedLocalsSchemaPath(), "plect:task:"+d.ID+":locals"); err != nil {
			return nil, fmt.Errorf("layer %q: locals schema: %w", d.ID, err)
		}
		out = append(out, layer)
	}
	return out, nil
}

// CleanupLayers builds the cleanup-relevant layer chain of a definition,
// skipping the schema compilation ResolveLayers does. Teardown must stay
// resilient to a definition whose config drifted to invalid after the
// instance was created, so it takes only what unwinding needs: each layer's
// cleanup, the file it came from, and the outward joint. The joint is not
// optional here — a layer's cleanup reads its own public contract, and that
// contract exists only because `[outputs.bind]` projects it, so a chain
// rebuilt without the bindings could never release what a layer produced as
// a private local. Classifying them needs no schema.
func CleanupLayers(def config.TaskDefinition) []ResolvedLayer {
	if !def.IsNested() {
		return nil
	}
	defs := append([]config.TaskDefinition{def}, def.InnerChain...)
	out := make([]ResolvedLayer, 0, len(defs))
	for _, d := range defs {
		out = append(out, ResolvedLayer{
			TaskID:      d.ID,
			Cleanup:     d.Cleanup,
			SourcePath:  d.SourcePath,
			From:        d.Ownership(),
			BindOutputs: d.ClassifiedOutputBindings(),
		})
	}
	return out
}

// runNestedSetup runs a nesting chain outside-in, returning one state record
// per layer that reached an outcome. The records are returned on failure too:
// a layer that produced must be unwound by the next cleanup, so the caller
// persists them either way.
func runNestedSetup(goCtx context.Context, layers []ResolvedLayer, base RenderContext, nodeInputs map[string]any) ([]contract.LayerState, []byte, error) {
	states := make([]contract.LayerState, 0, len(layers))
	inputs := orEmpty(nodeInputs)
	var env []string
	var lastStderr []byte

	for i, layer := range layers {
		now := time.Now()
		innermost := i == len(layers)-1
		// The outermost layer's inputs are the node's own, already validated
		// against the composed task's inputs schema by the caller; every
		// layer inward answers to its own schema for what the joint bound.
		if i > 0 && layer.InputsSchema != nil {
			if err := layer.InputsSchema.Validate(toJSONShape(inputs)); err != nil {
				return states, lastStderr, fmt.Errorf("layer %q: bound inputs: %w", layer.TaskID, err)
			}
		}
		state := contract.LayerState{EffectID: layer.TaskID, Inputs: inputs, SetupAt: now}
		ctx := base
		ctx.Self = map[string]any{}
		ctx.Inputs = inputs
		ctx.Locals = map[string]any{}
		ctx.SourcePath = layer.SourcePath

		emitted := map[string]any{}
		if layer.Setup != nil {
			resolved, resolveErr := resolveEffect(layer.Setup, setupRoots(ctx), ctx, layer.From, nil)
			if resolveErr != nil {
				state.Status = contract.TaskStatusFailed
				state.FailedAt = now
				state.Error = resolveErr.Error()
				return append(states, state), lastStderr, fmt.Errorf("layer %q setup: %w", layer.TaskID, resolveErr)
			}
			stdout, stderr, runErr := resolved.Run(goCtx, base.Session.WorkspaceDirPath, env...)
			resolved.Close()
			lastStderr = stderr
			if runErr != nil {
				state.Status = contract.TaskStatusFailed
				state.FailedAt = now
				state.Error = runErr.Error()
				return append(states, state), stderr, fmt.Errorf("layer %q setup: %w", layer.TaskID, runErr)
			}
			parsed, parseErr := ParseOutputs(stdout)
			if parseErr != nil {
				state.Status = contract.TaskStatusFailed
				state.FailedAt = now
				state.Error = parseErr.Error()
				return append(states, state), stderr, fmt.Errorf("layer %q setup: %w", layer.TaskID, parseErr)
			}
			emitted = parsed
		}
		if innermost {
			if layer.OutputsSchema != nil {
				if err := layer.OutputsSchema.Validate(emitted); err != nil {
					state.Status = contract.TaskStatusFailed
					state.FailedAt = now
					state.Error = err.Error()
					return append(states, state), lastStderr, fmt.Errorf("layer %q setup: outputs schema: %w", layer.TaskID, err)
				}
			}
			state.Outputs = emitted
		} else {
			if layer.LocalsSchema != nil {
				if err := layer.LocalsSchema.Validate(emitted); err != nil {
					state.Status = contract.TaskStatusFailed
					state.FailedAt = now
					state.Error = err.Error()
					return append(states, state), lastStderr, fmt.Errorf("layer %q setup: locals schema: %w", layer.TaskID, err)
				}
			}
			state.Locals = emitted
		}
		state.Status = contract.TaskStatusProduced

		if !innermost {
			jointCtx := ctx
			jointCtx.Locals = emitted
			// A joint that fails to render leaves this layer produced, not
			// failed: its setup ran and took effect, so the unwind still owes
			// it a cleanup. The node-level state carries why the chain stopped.
			jointEnv := innerRoots(jointCtx)
			injected, err := resolveValues(layer.InnerEnv, jointEnv, jointCtx, layer.From)
			if err != nil {
				return append(states, state), lastStderr, fmt.Errorf("layer %q inner.env: %w", layer.TaskID, err)
			}
			bound, err := resolveValues(layer.InnerInputs, jointEnv, jointCtx, layer.From)
			if err != nil {
				return append(states, state), lastStderr, fmt.Errorf("layer %q inner.inputs: %w", layer.TaskID, err)
			}
			state.Env = injected
			env = append(env, envAssignments(injected)...)
			inputs = make(map[string]any, len(bound))
			for k, v := range bound {
				inputs[k] = v
			}
		}
		states = append(states, state)
	}
	return states, lastStderr, nil
}

// runNestedCleanup unwinds a chain inside-out. A layer that never reached
// setup has no record here at all, which is what "cleanup skips layers that
// never reached setup" amounts to; every layer that does have a record is
// owed an attempt until it is cleaned, exactly as a plain task whose cleanup
// failed is re-attempted rather than written off. Like RunCleanup's own loop
// it keeps going past a failure — an inner layer that refuses to release must
// not strand the outer layers that wrap it — and returns the first error.
//
// A layer left uncleaned is always an error, even when nothing was run for
// it: the node's status is what `plect status` reads long after the
// observer's line has scrolled away, so `cleaned` must never be reachable
// while a record still owes a release.
func runNestedCleanup(goCtx context.Context, layers []ResolvedLayer, states []contract.LayerState, base RenderContext, obs Observer, scope, nodeID string) ([]byte, error) {
	var firstErr error
	var lastStderr []byte
	for i := len(states) - 1; i >= 0; i-- {
		state := &states[i]
		if state.Status == contract.TaskStatusCleaned {
			obs.OnSkip(scope, nodeID, fmt.Sprintf("layer %q: already cleaned", state.EffectID))
			continue
		}
		if i >= len(layers) || layers[i].TaskID != state.EffectID {
			// The definition's chain no longer matches what was set up.
			// Running a script from a layer that is not the one recorded
			// would release the wrong thing, so the record stands and the
			// debt stays visible.
			obs.OnSkip(scope, nodeID, fmt.Sprintf("layer %q: definition no longer declares this layer", state.EffectID))
			continue
		}
		now := time.Now()
		if layers[i].Cleanup == nil {
			state.Status = contract.TaskStatusCleaned
			state.CleanedAt = now
			continue
		}
		ctx := base
		// A layer's cleanup reads its own public contract, the same view its
		// probes and completion conditions are written against — which is
		// how a layer releases what its setup produced as a private local:
		// by projecting it. The cleanup surface observes no locals root of
		// its own.
		ctx.Self = layerSelf(layers, states, i)
		ctx.Inputs = state.Inputs
		ctx.SourcePath = layers[i].SourcePath
		resolved, resolveErr := resolveEffect(layers[i].Cleanup, cleanupRoots(ctx), ctx, layers[i].From, nil)
		if resolveErr != nil {
			state.Status = contract.TaskStatusFailed
			state.FailedAt = now
			state.Error = resolveErr.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("layer %q cleanup: %w", state.EffectID, resolveErr)
			}
			continue
		}
		_, stderr, runErr := resolved.Run(goCtx, base.Session.WorkspaceDirPath, EnclosingEnv(states, i)...)
		resolved.Close()
		lastStderr = stderr
		if runErr != nil {
			state.Status = contract.TaskStatusFailed
			state.FailedAt = now
			state.Error = runErr.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("layer %q cleanup: %w", state.EffectID, runErr)
			}
			continue
		}
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		state.Error = ""
	}
	for i := range states {
		if states[i].Status != contract.TaskStatusCleaned && firstErr == nil {
			firstErr = fmt.Errorf("layer %q is not released (%s)", states[i].EffectID, states[i].Status)
		}
	}
	return lastStderr, firstErr
}

// layerSelf is one layer's own public contract as of the recorded state. A
// chain whose records no longer line up with the declaration cannot be
// projected, so that layer falls back to what it recorded directly.
func layerSelf(layers []ResolvedLayer, states []contract.LayerState, i int) map[string]any {
	if len(states) != len(layers) {
		return states[i].Outputs
	}
	views, err := ProjectLayerOutputs(layers, states, SessionVars{})
	if err != nil {
		return states[i].Outputs
	}
	return views[i]
}

// EnclosingEnv is the process environment the layers outside index i inject
// into its executions: its setup and cleanup, and equally its probes, output
// scripts, and terminal operation commands.
func EnclosingEnv(states []contract.LayerState, i int) []string {
	var env []string
	for _, outer := range states[:i] {
		env = append(env, envAssignments(outer.Env)...)
	}
	return env
}

// envAssignments renders a bind.env map as sorted KEY=VALUE assignments;
// sorted so a recorded execution is reproducible rather than map-ordered.
func envAssignments(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// projectNestedOutputs renders the composed public contract a produced chain
// presents and holds it to the composed task's own outputs schema, the same
// obligation a plain task's setup output carries.
func projectNestedOutputs(r Resolved, states []contract.LayerState, session SessionVars) (map[string]any, error) {
	outputs, err := ProjectPublicOutputs(r.Layers, states, session)
	if err != nil {
		return nil, err
	}
	if r.OutputsSchema != nil {
		if vErr := r.OutputsSchema.Validate(outputs); vErr != nil {
			return nil, fmt.Errorf("outputs schema: %w", vErr)
		}
	}
	return outputs, nil
}
