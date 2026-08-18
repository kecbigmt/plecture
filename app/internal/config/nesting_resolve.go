package config

import (
	"fmt"
	"sort"
	"strings"
)

// nestingIndex is the reference namespace `inner` resolves against: the
// merged task namespace for a plain id, and the per-plugin namespaces a
// catalog-qualified reference selects from without consulting same-id user
// shadows.
type nestingIndex struct {
	merged   map[string]TaskDefinition
	byPlugin map[string]map[string]TaskDefinition
	// owner maps a definition's own file path to the plugin that mounted it,
	// which is what makes a plugin's relative `inner` resolve in its own
	// namespace instead of the merged one.
	owner map[string]string
	// missingPlugin names a plugin to enable for an otherwise unresolvable
	// qualified reference, mirroring the {{bin}} remediation hint.
	missingPlugin func(ref string) string
}

func (ix nestingIndex) resolve(from TaskDefinition, ref string) (TaskDefinition, error) {
	if strings.Contains(ref, "/") {
		return ix.resolveQualified(ref)
	}
	if pluginID, ok := ix.owner[from.SourcePath]; ok {
		def, found := ix.byPlugin[pluginID][ref]
		if !found {
			return TaskDefinition{}, fmt.Errorf("inner %q: plugin %q declares no such task (a plugin's relative inner reference resolves in its own namespace)", ref, pluginID)
		}
		return def, nil
	}
	def, found := ix.merged[ref]
	if !found {
		return TaskDefinition{}, fmt.Errorf("inner names unknown task %q", ref)
	}
	return def, nil
}

// resolveQualified reads ref as "<catalog-alias>/<plugin-path>/<task-id>"
// with the accept-exactly-one semantics qualified {{bin}} references already
// use, so a plugin path of arbitrary depth never rests on a longest-prefix
// guess.
func (ix nestingIndex) resolveQualified(ref string) (TaskDefinition, error) {
	var candidates []TaskDefinition
	var from []string
	pluginIDs := make([]string, 0, len(ix.byPlugin))
	for id := range ix.byPlugin {
		pluginIDs = append(pluginIDs, id)
	}
	sort.Strings(pluginIDs)
	for _, id := range pluginIDs {
		if !strings.HasPrefix(ref, id+"/") {
			continue
		}
		if def, ok := ix.byPlugin[id][strings.TrimPrefix(ref, id+"/")]; ok {
			candidates = append(candidates, def)
			from = append(from, id)
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		hint := ""
		if ix.missingPlugin != nil {
			hint = ix.missingPlugin(ref)
		}
		return TaskDefinition{}, fmt.Errorf("inner %q: no enabled plugin resolves this task reference (want \"<catalog-alias>/<plugin-path>/<task-id>\")%s", ref, hint)
	default:
		return TaskDefinition{}, fmt.Errorf("inner %q: ambiguous; matches more than one plugin reading: %v", ref, from)
	}
}

// nestingChain walks inward from def, returning the chain next-inner first.
func (ix nestingIndex) nestingChain(def TaskDefinition) ([]TaskDefinition, error) {
	var chain []TaskDefinition
	seen := map[string]bool{def.SourcePath: true}
	cur := def
	for cur.IsNested() {
		next, err := ix.resolve(cur, cur.Inner)
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

// resolveNestedDefinitions resolves and validates every nesting chain among
// the loaded definitions, stamping the resolved chain onto each outer task in
// merged. all carries every definition file that was read, including plugin
// definitions a same-id user task shadows, so a qualified `inner` can still
// reach one.
func resolveNestedDefinitions(merged map[string]TaskDefinition, all []TaskDefinition, owner map[string]string, missingPlugin func(string) string) error {
	byPlugin := make(map[string]map[string]TaskDefinition)
	for _, def := range all {
		pluginID, ok := owner[def.SourcePath]
		if !ok {
			continue
		}
		if byPlugin[pluginID] == nil {
			byPlugin[pluginID] = make(map[string]TaskDefinition)
		}
		byPlugin[pluginID][def.ID] = def
	}
	ix := nestingIndex{merged: merged, byPlugin: byPlugin, owner: owner, missingPlugin: missingPlugin}

	sorted := append([]TaskDefinition(nil), all...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SourcePath < sorted[j].SourcePath })
	for _, def := range sorted {
		if !def.IsNested() {
			continue
		}
		chain, err := ix.nestingChain(def)
		if err != nil {
			return fmt.Errorf("task %s: %w", def.SourcePath, err)
		}
		layers := append([]TaskDefinition{def}, chain...)
		if err := validateNesting(layers); err != nil {
			return fmt.Errorf("task %s: %w", def.SourcePath, err)
		}
		// Only the outermost definition carries the chain: it is already
		// flattened, so re-stamping each inner layer would store the same
		// suffix N times.
		if cur, ok := merged[def.ID]; ok && cur.SourcePath == def.SourcePath {
			cur.InnerChain = chain
			merged[def.ID] = cur
		}
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
	if err := validateChainJudgeIDs(layers); err != nil {
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
	if err := validateComposedDoneWhen(layers, bindings, exposure); err != nil {
		return err
	}
	return validateComposedChains(layers, exposure)
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

func validateChainJudgeIDs(layers []TaskDefinition) error {
	owner := map[string]string{}
	for _, layer := range layers {
		if layer.DoneWhen == nil {
			continue
		}
		for _, leaf := range layer.DoneWhen.All {
			if leaf.ID == "" {
				continue
			}
			if prev, dup := owner[leaf.ID]; dup {
				return fmt.Errorf("judge id %q is declared by both layer %q and layer %q; a verdict targets an id, so the id must be unique across the nesting chain", leaf.ID, prev, layer.ID)
			}
			owner[leaf.ID] = layer.ID
		}
	}
	return nil
}

func validateChainEnv(layers []TaskDefinition) error {
	owner := map[string]string{}
	for _, layer := range layers {
		keys := make([]string, 0, len(layer.Bind.EnvBindings()))
		for k := range layer.Bind.EnvBindings() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !envNameRE.MatchString(k) {
				return fmt.Errorf("layer %q: bind.env key %q is not a valid process environment name (must match %s)", layer.ID, k, envNameRE.String())
			}
			if prev, dup := owner[k]; dup {
				return fmt.Errorf("bind.env key %q is declared by both layer %q and layer %q; env keys are unique across the nesting chain", k, prev, layer.ID)
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
	if err := ValidateDynamicOutputs(outer.DynamicOutputs); err != nil {
		return nil, err
	}
	bound := outer.Bind.OutputBindings()
	for _, o := range outer.DynamicOutputs {
		for _, name := range o.OutputNames() {
			if _, clash := bound[name]; clash {
				return nil, fmt.Errorf("produced output %q collides with the [bind.outputs] key of the same name; one public name has one definition source", name)
			}
			if isBuiltinDynamicOutput(name) {
				continue
			}
			if _, declared := outerProps[name]; !declared {
				return nil, fmt.Errorf("produced output %q is missing from this layer's outputs_schema", name)
			}
		}
	}
	if err := ValidateTaskRequires(outer); err != nil {
		return nil, err
	}

	bindings, err := outer.ClassifiedOutputBindings()
	if err != nil {
		return nil, err
	}
	for _, b := range bindings {
		key := b.Key
		if _, declared := outerProps[key]; !declared {
			return nil, fmt.Errorf("bind.outputs declares public key %q, which is missing from this layer's outputs_schema", key)
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
				return nil, fmt.Errorf("bind.outputs %q binds inner output %q of type %q but declares type %q; a direct binding projects the inner value natively", key, b.InnerKey, innerType, declaredType)
			}
			if mutable && !schemaPropertyMutable(innerProps, b.InnerKey) {
				return nil, fmt.Errorf("bind.outputs %q is declared mutable but binds the immutable inner output %q", key, b.InnerKey)
			}
			continue
		}
		if mutable {
			return nil, fmt.Errorf("bind.outputs %q is a computed template and cannot be declared mutable; a mutable write has no inner output to route to", key)
		}
		if hasType && declaredType != "string" {
			return nil, fmt.Errorf("bind.outputs %q is a computed template, so its outputs_schema type must be \"string\", not %q", key, declaredType)
		}
	}
	if err := validateBoundInputs(outer, inner); err != nil {
		return nil, err
	}
	return bindings, nil
}

func validateBoundInputs(outer, inner TaskDefinition) error {
	bound := outer.Bind.InputBindings()
	required, err := SchemaRequiredNames(inner.InputsSchema, inner.ResolvedInputsSchemaPath())
	if err != nil {
		return fmt.Errorf("inner task %q inputs schema: %w", inner.ID, err)
	}
	for _, r := range required {
		if _, ok := bound[r]; !ok {
			return fmt.Errorf("bind.inputs omits inner required input %q of task %q", r, inner.ID)
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
			return fmt.Errorf("bind.inputs binds %q, which the closed inputs schema of inner task %q rejects", k, inner.ID)
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

// validateComposedDoneWhen keeps every layer's completion conditions readable
// from the composed public contract. An inner layer's check additionally has
// to read the inner output itself: a computed binding in its place would let
// the outer layer choose what the inner gate sees.
func validateComposedDoneWhen(layers []TaskDefinition, bindings [][]OutputBinding, exposure []map[string]string) error {
	for i, layer := range layers {
		if layer.DoneWhen == nil {
			continue
		}
		for _, leaf := range layer.DoneWhen.All {
			key := strings.TrimSpace(leaf.Check)
			if key == "" {
				continue
			}
			if _, ok := exposure[i][key]; ok {
				continue
			}
			if i > 0 && referencedByBinding(bindings[i-1], key) {
				return fmt.Errorf("layer %q done_when reads output %q, which layer %q binds by a computed template; an inner condition's value must come from a direct binding of that inner output", layer.ID, key, layers[i-1].ID)
			}
			return fmt.Errorf("layer %q done_when reads output %q, which the composed public contract does not bind", layer.ID, key)
		}
	}
	return nil
}

func referencedByBinding(bindings []OutputBinding, innerKey string) bool {
	for _, b := range bindings {
		for _, ref := range b.InnerRefs {
			if ref == innerKey {
				return true
			}
		}
	}
	return false
}

// validateComposedChains resolves every layer's chain references — the
// innermost plugin task's as much as the outermost's — against the judge
// namespace of the composed effective done_when and against the composed
// public contract.
//
// A chain an inner layer declared reads nothing the outer layers did not
// bind, and it names that output by its own layer's name for it: references
// are layer-explicit, so an inner author's declarations keep working when an
// outer layer renames what it re-exports. The composed contract decides
// reachability, not spelling — which is why the reachable set is the layer's
// own keys that project outward, and why evaluating those declarations
// against a composed instance resolves each key through the same exposure map
// that proved it reachable here.
func validateComposedChains(layers []TaskDefinition, exposure []map[string]string) error {
	judgeIDs := map[string]bool{}
	for _, layer := range layers {
		if layer.DoneWhen == nil {
			continue
		}
		for _, leaf := range layer.DoneWhen.All {
			if leaf.ID != "" {
				judgeIDs[leaf.ID] = true
			}
		}
	}
	for i, layer := range layers {
		if len(layer.Chains) == 0 {
			continue
		}
		reachable := make(map[string]bool, len(exposure[i]))
		for key := range exposure[i] {
			reachable[key] = true
		}
		if err := validateChainReferences(layer, judgeIDs, reachable, true); err != nil {
			return fmt.Errorf("layer %q: %w", layer.ID, err)
		}
	}
	return nil
}

func isBuiltinDynamicOutput(name string) bool {
	for _, b := range builtinDynamicOutputs {
		if b.Name == name {
			return true
		}
	}
	return false
}
