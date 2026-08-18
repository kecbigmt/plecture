package task

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
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
	Setup      string
	Cleanup    string
	SourcePath string
	// BindInputs / BindEnv are this layer's joint toward the next layer
	// inward: templates producing that layer's input object, and the process
	// environment added to every execution inside this layer.
	BindInputs map[string]string
	BindEnv    map[string]string
	// BindOutputs is this layer's joint outward: the classified
	// `[bind.outputs]` entries that project the layer inside it into this
	// layer's own public contract.
	BindOutputs []config.OutputBinding
	// InputsSchema validates the inputs this layer is set up with, and
	// LocalsSchema the intermediates its setup emits. OutputsSchema is set
	// only for the innermost layer, whose setup emits the chain's actual
	// task outputs rather than locals.
	InputsSchema  *jsonschema.Schema
	LocalsSchema  *jsonschema.Schema
	OutputsSchema *jsonschema.Schema
	// Health, DoneWhen, DynamicOutputs, and Terminal are what this layer
	// declares for itself. They compose across the chain rather than
	// override: alive by AND, activity by OR, done_when by conjunction,
	// produced outputs into this layer's own contract, and at most one
	// [terminal] per chain.
	Health         *config.HealthConfig
	DoneWhen       *config.DoneWhen
	DynamicOutputs []config.DynamicOutput
	Terminal       *config.TerminalConfig
	Chains         []config.ChainDefinition
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
			TaskID:         d.ID,
			Setup:          d.Setup,
			Cleanup:        d.Cleanup,
			SourcePath:     d.SourcePath,
			BindInputs:     d.Bind.InputBindings(),
			BindEnv:        d.Bind.EnvBindings(),
			Health:         d.Health,
			DoneWhen:       d.DoneWhen,
			DynamicOutputs: d.DynamicOutputs,
			Chains:         d.Chains,
		}
		if d.Terminal.IsDeclared() {
			layer.Terminal = d.Terminal
		}
		bindings, err := d.ClassifiedOutputBindings()
		if err != nil {
			return nil, fmt.Errorf("layer %q: %w", d.ID, err)
		}
		layer.BindOutputs = bindings
		if layer.InputsSchema, err = CompileSchema(d.InputsSchema, d.ResolvedInputsSchemaPath(), "plect:task:"+d.ID+":inputs"); err != nil {
			return nil, fmt.Errorf("layer %q: input schema: %w", d.ID, err)
		}
		if i == len(defs)-1 {
			if layer.OutputsSchema, err = CompileSchema(d.OutputsSchema, d.ResolvedOutputsSchemaPath(), "plect:task:"+d.ID+":outputs"); err != nil {
				return nil, fmt.Errorf("layer %q: outputs schema: %w", d.ID, err)
			}
		} else if layer.LocalsSchema, err = CompileSchema(d.LocalsSchema, d.ResolvedLocalsSchemaPath(), "plect:task:"+d.ID+":locals"); err != nil {
			return nil, fmt.Errorf("layer %q: locals schema: %w", d.ID, err)
		}
		out = append(out, layer)
	}
	return out, nil
}

// CleanupLayers builds the cleanup-relevant layer chain of a definition,
// skipping the schema compilation resolveLayers does. Teardown must stay
// resilient to a definition whose config drifted to invalid after the
// instance was created, and unwinding needs only each layer's script and the
// file it came from — every value it renders against was persisted at setup.
func CleanupLayers(def config.TaskDefinition) []ResolvedLayer {
	if !def.IsNested() {
		return nil
	}
	defs := append([]config.TaskDefinition{def}, def.InnerChain...)
	out := make([]ResolvedLayer, 0, len(defs))
	for _, d := range defs {
		out = append(out, ResolvedLayer{TaskID: d.ID, Cleanup: d.Cleanup, SourcePath: d.SourcePath})
	}
	return out
}

// runNestedSetup runs a nesting chain outside-in, returning one state record
// per layer that reached an outcome. The records are returned on failure too:
// a layer that produced must be unwound by the next cleanup, so the caller
// persists them either way.
func runNestedSetup(goCtx context.Context, layers []ResolvedLayer, base RenderContext, nodeInputs map[string]any) ([]contract.TaskLayerState, []byte, error) {
	states := make([]contract.TaskLayerState, 0, len(layers))
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
		state := contract.TaskLayerState{TaskID: layer.TaskID, Inputs: inputs, SetupAt: now}
		ctx := base
		ctx.Self = map[string]any{}
		ctx.Inputs = inputs
		ctx.Locals = map[string]any{}
		ctx.SourcePath = layer.SourcePath

		cmdStr, err := render(layer.Setup, ctx)
		if err != nil {
			state.Status = contract.TaskStatusFailed
			state.FailedAt = now
			state.Error = err.Error()
			return append(states, state), lastStderr, fmt.Errorf("layer %q setup template: %w", layer.TaskID, err)
		}
		emitted := map[string]any{}
		if strings.TrimSpace(cmdStr) != "" {
			stdout, stderr, runErr := execHostScript(goCtx, cmdStr, base.Session.WorkspaceDirPath, env...)
			lastStderr = stderr
			if runErr != nil {
				state.Status = contract.TaskStatusFailed
				state.FailedAt = now
				state.Error = runErr.Error()
				return append(states, state), stderr, fmt.Errorf("layer %q setup: %w", layer.TaskID, runErr)
			}
			emitted, err = ParseOutputs(stdout)
			if err != nil {
				state.Status = contract.TaskStatusFailed
				state.FailedAt = now
				state.Error = err.Error()
				return append(states, state), stderr, fmt.Errorf("layer %q setup: %w", layer.TaskID, err)
			}
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
			injected, err := renderBindings(layer.BindEnv, jointCtx)
			if err != nil {
				return append(states, state), lastStderr, fmt.Errorf("layer %q bind.env: %w", layer.TaskID, err)
			}
			bound, err := renderBindings(layer.BindInputs, jointCtx)
			if err != nil {
				return append(states, state), lastStderr, fmt.Errorf("layer %q bind.inputs: %w", layer.TaskID, err)
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
func runNestedCleanup(goCtx context.Context, layers []ResolvedLayer, states []contract.TaskLayerState, base RenderContext, obs Observer, scope, nodeID string) ([]byte, error) {
	var firstErr error
	var lastStderr []byte
	for i := len(states) - 1; i >= 0; i-- {
		state := &states[i]
		if state.Status == contract.TaskStatusCleaned {
			obs.OnSkip(scope, nodeID, fmt.Sprintf("layer %q: already cleaned", state.TaskID))
			continue
		}
		if i >= len(layers) || layers[i].TaskID != state.TaskID {
			// The definition's chain no longer matches what was set up.
			// Running a script from a layer that is not the one recorded
			// would release the wrong thing, so the record stands and the
			// debt stays visible.
			obs.OnSkip(scope, nodeID, fmt.Sprintf("layer %q: definition no longer declares this layer", state.TaskID))
			continue
		}
		now := time.Now()
		if strings.TrimSpace(layers[i].Cleanup) == "" {
			state.Status = contract.TaskStatusCleaned
			state.CleanedAt = now
			continue
		}
		ctx := base
		ctx.Self = state.Outputs
		ctx.Inputs = state.Inputs
		ctx.Locals = state.Locals
		ctx.SourcePath = layers[i].SourcePath
		cmdStr, err := renderCleanup(layers[i].Cleanup, ctx)
		if err != nil {
			state.Status = contract.TaskStatusFailed
			state.FailedAt = now
			state.Error = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("layer %q cleanup template: %w", state.TaskID, err)
			}
			continue
		}
		_, stderr, runErr := execHostScript(goCtx, cmdStr, base.Session.WorkspaceDirPath, EnclosingEnv(states, i)...)
		lastStderr = stderr
		if runErr != nil {
			state.Status = contract.TaskStatusFailed
			state.FailedAt = now
			state.Error = runErr.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("layer %q cleanup: %w", state.TaskID, runErr)
			}
			continue
		}
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		state.Error = ""
	}
	for i := range states {
		if states[i].Status != contract.TaskStatusCleaned && firstErr == nil {
			firstErr = fmt.Errorf("layer %q is not released (%s)", states[i].TaskID, states[i].Status)
		}
	}
	return lastStderr, firstErr
}

// EnclosingEnv is the process environment the layers outside index i inject
// into its executions: its setup and cleanup, and equally its probes, output
// scripts, and terminal operation commands.
func EnclosingEnv(states []contract.TaskLayerState, i int) []string {
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

// renderBindings renders one `[bind.*]` table under the same strict
// missing-key semantics setup bodies use: a binding reaching for a local its
// layer never emitted is a wiring error, not an empty string.
func renderBindings(bindings map[string]string, ctx RenderContext) (map[string]string, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(bindings))
	for k := range bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(bindings))
	for _, k := range keys {
		rendered, err := render(bindings[k], ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = rendered
	}
	return out, nil
}

// projectNestedOutputs renders the composed public contract a produced chain
// presents and holds it to the composed task's own outputs schema, the same
// obligation a plain task's setup output carries.
func projectNestedOutputs(r Resolved, states []contract.TaskLayerState, session SessionVars) (map[string]any, error) {
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
