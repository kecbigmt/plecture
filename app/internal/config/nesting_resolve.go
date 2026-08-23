package config

import (
	"fmt"
	"sort"
)

// nestingIndex is the namespace `inner` resolves against: every loaded effect,
// keyed by the address it answers to. A reference has already been read where
// it was written by the time it arrives here, so resolution is a lookup —
// nesting shares the one reference grammar rather than carrying a second.
type nestingIndex struct {
	byAddress map[string]TaskDefinition
	// missingPlugin names a plugin to enable for an otherwise unresolvable
	// qualified reference, mirroring the executable reference's remediation hint.
	missingPlugin func(ref string) string
}

func (ix nestingIndex) resolve(ref string) (TaskDefinition, error) {
	if def, ok := ix.byAddress[ref]; ok {
		return def, nil
	}
	hint := ""
	if ix.missingPlugin != nil {
		hint = ix.missingPlugin(ref)
	}
	return TaskDefinition{}, fmt.Errorf("inner names unknown effect %q%s", ref, hint)
}

// nestingChain walks inward from def, returning the chain next-inner first.
func (ix nestingIndex) nestingChain(def TaskDefinition) ([]TaskDefinition, error) {
	var chain []TaskDefinition
	seen := map[string]bool{def.SourcePath: true}
	cur := def
	for cur.IsNested() {
		next, err := ix.resolve(cur.Inner)
		if err != nil {
			return nil, err
		}
		if seen[next.SourcePath] {
			return nil, fmt.Errorf("inner %q forms a nesting cycle", cur.Inner)
		}
		seen[next.SourcePath] = true
		chain = append(chain, next)
		cur = next
	}
	return chain, nil
}

// resolveNestedDefinitions resolves and validates every nesting chain among the
// loaded definitions, stamping the resolved chain onto each outer effect.
func resolveNestedDefinitions(defs map[string]TaskDefinition, missingPlugin func(string) string) error {
	ix := nestingIndex{byAddress: defs, missingPlugin: missingPlugin}
	addresses := make([]string, 0, len(defs))
	for address := range defs {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		def := defs[address]
		if !def.IsNested() {
			continue
		}
		chain, err := ix.nestingChain(def)
		if err != nil {
			return fmt.Errorf("task %s: %w", def.SourcePath, err)
		}
		if err := validateNesting(append([]TaskDefinition{def}, chain...)); err != nil {
			return fmt.Errorf("task %s: %w", def.SourcePath, err)
		}
		// Only the outermost definition carries the chain: it is already
		// flattened, so re-stamping each inner layer would store the same
		// suffix N times.
		def.InnerChain = chain
		defs[address] = def
	}
	return nil
}

// validateNesting is the load-error rule suite of
// docs/design/task-nesting.md's Validation Rules section, applied to one
// chain with layers[0] the outermost task and the last element the innermost.
func validateNesting(layers []TaskDefinition) error {
	if err := validateChainScope(layers); err != nil {
		return err
	}
	if err := validateChainTerminalCount(layers); err != nil {
		return err
	}
	if err := validateChainEnv(layers); err != nil {
		return err
	}
	bindings := make([][]OutputBinding, len(layers))
	for i := 0; i+1 < len(layers); i++ {
		b, err := validateLayer(layers[i], layers[i+1])
		if err != nil {
			return fmt.Errorf("layer %q: %w", layers[i].ID, err)
		}
		bindings[i] = b
	}
	exposure, err := composedExposure(layers, bindings)
	if err != nil {
		return err
	}
	if err := validateChainTerminalEndpoint(layers, bindings, exposure); err != nil {
		return err
	}
	return nil
}

func validateChainScope(layers []TaskDefinition) error {
	innermost := layers[len(layers)-1]
	scope := innermost.EffectiveScope()
	for _, layer := range layers[:len(layers)-1] {
		if layer.Scope != "" && layer.Scope != scope {
			return fmt.Errorf("layer %q declares scope %q, which differs from the inner task %q scope %q", layer.ID, layer.Scope, innermost.ID, scope)
		}
	}
	return nil
}

func validateChainTerminalCount(layers []TaskDefinition) error {
	var declaring []string
	for _, layer := range layers {
		if layer.Terminal.IsDeclared() {
			declaring = append(declaring, layer.ID)
		}
	}
	if len(declaring) > 1 {
		return fmt.Errorf("layers %v both declare [terminal]; a nesting chain admits at most one", declaring)
	}
	return nil
}

func validateChainEnv(layers []TaskDefinition) error {
	owner := map[string]string{}
	for _, layer := range layers {
		keys := make([]string, 0, len(layer.InnerEnv))
		for k := range layer.InnerEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !envNameRE.MatchString(k) {
				return fmt.Errorf("layer %q: inner.env key %q is not a valid process environment name (must match %s)", layer.ID, k, envNameRE.String())
			}
			if prev, dup := owner[k]; dup {
				return fmt.Errorf("inner.env key %q is declared by both layer %q and layer %q; env keys are unique across the nesting chain", k, prev, layer.ID)
			}
			owner[k] = layer.ID
		}
	}
	return nil
}

// validateLayer checks one outer layer against its next inner task and
// returns its classified `[bind.outputs]` entries.
func validateLayer(outer, inner TaskDefinition) ([]OutputBinding, error) {
	outerProps, err := SchemaProperties(outer.OutputsSchema, outer.ResolvedOutputsSchemaPath())
	if err != nil {
		return nil, fmt.Errorf("outputs schema: %w", err)
	}
	innerProps, err := SchemaProperties(inner.OutputsSchema, inner.ResolvedOutputsSchemaPath())
	if err != nil {
		return nil, fmt.Errorf("inner task %q outputs schema: %w", inner.ID, err)
	}
	bindings := outer.ClassifiedOutputBindings()
	for _, b := range bindings {
		key := b.Key
		if _, declared := outerProps[key]; !declared {
			return nil, fmt.Errorf("outputs.bind declares public key %q, which is missing from this layer's outputs_schema", key)
		}
		declaredType, hasType := schemaPropertyType(outerProps, key)
		mutable := schemaPropertyMutable(outerProps, key)
		// The bound inner key itself is not checked for existence: a task's
		// outputs are not exhaustively declared — setup stdout may carry keys
		// an open outputs_schema never lists, and `[[outputs]]`-produced keys
		// are not schema properties at all — so an unknown-key rule would
		// reject working configurations. The type and mutability rules below
		// consequently apply only where the inner layer declared something to
		// disagree with.
		if b.Direct {
			innerType, innerHasType := schemaPropertyType(innerProps, b.InnerKey)
			if hasType && innerHasType && declaredType != innerType {
				return nil, fmt.Errorf("outputs.bind %q binds inner output %q of type %q but declares type %q; a direct binding projects the inner value natively", key, b.InnerKey, innerType, declaredType)
			}
			if mutable && !schemaPropertyMutable(innerProps, b.InnerKey) {
				return nil, fmt.Errorf("outputs.bind %q is declared mutable but binds the immutable inner output %q", key, b.InnerKey)
			}
			continue
		}
		if mutable {
			return nil, fmt.Errorf("outputs.bind %q is computed and cannot be declared mutable; a mutable write has no inner output to route to", key)
		}
	}
	if err := validateBoundInputs(outer, inner); err != nil {
		return nil, err
	}
	return bindings, nil
}

func validateBoundInputs(outer, inner TaskDefinition) error {
	bound := outer.InnerInputs
	required, err := SchemaRequiredNames(inner.InputsSchema, inner.ResolvedInputsSchemaPath())
	if err != nil {
		return fmt.Errorf("inner task %q inputs schema: %w", inner.ID, err)
	}
	for _, r := range required {
		if _, ok := bound[r]; !ok {
			return fmt.Errorf("inner.inputs omits inner required input %q of effect %q", r, inner.ID)
		}
	}
	closed, err := SchemaIsClosed(inner.InputsSchema, inner.ResolvedInputsSchemaPath())
	if err != nil {
		return fmt.Errorf("inner task %q inputs schema: %w", inner.ID, err)
	}
	if !closed {
		return nil
	}
	props, err := SchemaProperties(inner.InputsSchema, inner.ResolvedInputsSchemaPath())
	if err != nil {
		return fmt.Errorf("inner task %q inputs schema: %w", inner.ID, err)
	}
	keys := make([]string, 0, len(bound))
	for k := range bound {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := props[k]; !ok {
			return fmt.Errorf("inner.inputs binds %q, which the closed inputs schema of inner effect %q rejects", k, inner.ID)
		}
	}
	return nil
}

// composedExposure maps, for each layer, the layer's own public output keys
// that reach the composed public contract through an unbroken run of direct
// bindings, to the public name they arrive under.
func composedExposure(layers []TaskDefinition, bindings [][]OutputBinding) ([]map[string]string, error) {
	exposure := make([]map[string]string, len(layers))
	outerProps, err := SchemaProperties(layers[0].OutputsSchema, layers[0].ResolvedOutputsSchemaPath())
	if err != nil {
		return nil, fmt.Errorf("layer %q: outputs schema: %w", layers[0].ID, err)
	}
	exposure[0] = make(map[string]string, len(outerProps))
	for k := range outerProps {
		exposure[0][k] = k
	}
	for i := 0; i+1 < len(layers); i++ {
		exposure[i+1] = map[string]string{}
		for _, b := range bindings[i] {
			if !b.Direct {
				continue
			}
			if publicKey, ok := exposure[i][b.Key]; ok {
				exposure[i+1][b.InnerKey] = publicKey
			}
		}
	}
	return exposure, nil
}

func validateChainTerminalEndpoint(layers []TaskDefinition, bindings [][]OutputBinding, exposure []map[string]string) error {
	declaring := -1
	for i, layer := range layers {
		if layer.Terminal.IsDeclared() {
			declaring = i
			break
		}
	}
	if declaring < 0 {
		return nil
	}
	if declaring == 0 {
		for _, b := range bindings[0] {
			if b.Key == OutputKeyInteractiveEndpoint && !b.Direct && len(b.InnerRefs) == 0 {
				return nil
			}
		}
		return fmt.Errorf("layer %q declares [terminal] but the composed public contract does not bind %q from that layer's own locals", layers[0].ID, OutputKeyInteractiveEndpoint)
	}
	if exposure[declaring][OutputKeyInteractiveEndpoint] == OutputKeyInteractiveEndpoint {
		return nil
	}
	return fmt.Errorf("layer %q declares [terminal] but the composed public contract does not bind %q from that layer", layers[declaring].ID, OutputKeyInteractiveEndpoint)
}
