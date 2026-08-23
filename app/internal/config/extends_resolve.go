package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// composeExtendedTaskDocuments materializes the effective declaration for
// every document that declares extends. It runs inside LoadTaskDocuments,
// before any caller decides whether to also run the heavier
// ValidateTaskDocuments contract pass, so it validates the extends-specific
// composition rules itself — chain resolution, judge-id and chain-id
// uniqueness, and the schema key rules — through lang's own exported
// checkers rather than trusting a validation pass that may not have run yet.
// ValidateTaskContracts runs the identical checkers against the same chain
// later, so there remains one authority for what the rules are; this is a
// second call site, not a second implementation.
func composeExtendedTaskDocuments(docs map[string]TaskDocument, registry *lang.Registry) error {
	// original is an immutable snapshot: composing one document must read
	// every ancestor's own, single-layer contribution, never another
	// document's already-composed result, or a chain longer than two would
	// double-count whichever ancestor address sorts ahead of it.
	original := make(map[string]TaskDocument, len(docs))
	byDef := make(map[*lang.Definition]string, len(docs))
	for address, doc := range docs {
		original[address] = doc
		byDef[doc.Definition] = address
	}
	addresses := make([]string, 0, len(docs))
	for address := range docs {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		doc := original[address]
		if doc.Extends == "" {
			continue
		}
		v := lang.Validation{From: doc.Ownership()}
		chainDefs, err := v.ExtendsChain(doc.Definition, registry)
		if err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}
		defLayers := append(append([]*lang.Definition{}, chainDefs...), doc.Definition)
		if _, err := lang.SchemaKeyRules("inputs_schema", defLayers); err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}
		if _, err := lang.SchemaKeyRules("state_schema", defLayers); err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}
		if _, err := lang.ComposedJudges(defLayers); err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}
		if _, err := lang.ComposedChainIDs(defLayers); err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}

		layers := make([]TaskDocument, 0, len(chainDefs)+1)
		for _, def := range chainDefs {
			layerAddress, ok := byDef[def]
			if !ok {
				return fmt.Errorf("task %s: extends chain reaches a definition outside the loaded task documents", address)
			}
			layers = append(layers, original[layerAddress])
		}
		layers = append(layers, doc)
		if err := rejectSchemaFileInChain(layers); err != nil {
			return fmt.Errorf("task %s: %w", address, err)
		}
		docs[address] = composeTaskDocument(layers)
	}
	return nil
}

// rejectSchemaFileInChain fails loud rather than silently dropping a
// contract: mergeSchemaField reads a layer's inline inputs_schema/
// state_schema table only, so a layer that instead points at
// inputs_schema_file/state_schema_file — resolved relative to that layer's
// own directory, which a composed document has no per-field way to
// remember — composes into nothing rather than the contract it names.
func rejectSchemaFileInChain(layers []TaskDocument) error {
	for _, layer := range layers {
		if layer.InputsSchemaFile != "" {
			return fmt.Errorf("task %q declares inputs_schema_file, which extends composition does not support; declare inputs_schema inline", layer.ID)
		}
		if layer.StateSchemaFile != "" {
			return fmt.Errorf("task %q declares state_schema_file, which extends composition does not support; declare state_schema inline", layer.ID)
		}
	}
	return nil
}

// composeTaskDocument merges an extends chain's layers, root first and the
// extension itself last, into the single effective document every runtime
// consumer reads: instructions and chains append in that order, done_when
// leaves append, and inputs_schema/state_schema properties merge by key. The
// result keeps the outermost (most specific) layer's own identity, ownership,
// and source.
func composeTaskDocument(layers []TaskDocument) TaskDocument {
	composed := layers[len(layers)-1]
	// An extension declares no resource_observer of its own (lang's structural
	// check already guarantees this); it inherits the root's.
	composed.ResourceObserver = layers[0].ResourceObserver

	var texts []string
	var chains []DocumentChain
	var leaves []DoneWhenLeaf
	for _, layer := range layers {
		if layer.Instruction != "" {
			texts = append(texts, layer.Instruction)
		}
		chains = append(chains, layer.Chains...)
		if layer.DoneWhen != nil {
			leaves = append(leaves, layer.DoneWhen.All...)
		}
	}
	composed.Instruction = strings.Join(texts, "\n\n")
	composed.Chains = chains
	if len(leaves) > 0 {
		composed.DoneWhen = &DoneWhen{All: leaves, Budget: composed.Budget}
	}
	composed.InputsSchema = mergeSchemaField(layers, func(d TaskDocument) map[string]any { return d.InputsSchema })
	composed.StateSchema = mergeSchemaField(layers, func(d TaskDocument) map[string]any { return d.StateSchema })
	composed.ExtendsLayers = layers
	return composed
}

// mergeSchemaField merges one schema field (inputs_schema or state_schema)
// across an extends chain. `properties` is the union across every layer.
// `required` is the union too — an extension may only ever add a requirement,
// consistent with the chain's own append-only rule, never drop one a base
// declared. `additionalProperties` stays closed if any layer closed it: a
// composed key any layer added is already present in the merged `properties`
// either way, so closing loses nothing, while treating a later layer's silent
// omission as reopening would weaken a constraint the base fixed without any
// layer saying so. Every other top-level key (`type`, and the like) comes
// from the first layer, root first, that declares this field at all — the
// base's own shape, which an extension augments rather than restates.
func mergeSchemaField(layers []TaskDocument, field func(TaskDocument) map[string]any) map[string]any {
	merged := map[string]any{}
	present := false
	requiredSeen := map[string]bool{}
	var required []string
	closed := false
	for _, layer := range layers {
		schema := field(layer)
		if schema == nil {
			continue
		}
		present = true
		for k, v := range schema {
			switch k {
			case "properties", "required", "additionalProperties":
				continue
			default:
				if _, already := merged[k]; !already {
					merged[k] = v
				}
			}
		}
		for _, name := range schemaRequiredNames(schema) {
			if !requiredSeen[name] {
				requiredSeen[name] = true
				required = append(required, name)
			}
		}
		if additionalProperties, ok := schema["additionalProperties"].(bool); ok && !additionalProperties {
			closed = true
		}
	}
	if !present {
		return nil
	}
	merged["properties"] = mergeSchemaProperties(layers, field)
	if len(required) > 0 {
		sort.Strings(required)
		reqs := make([]any, len(required))
		for i, name := range required {
			reqs[i] = name
		}
		merged["required"] = reqs
	}
	if closed {
		merged["additionalProperties"] = false
	}
	return merged
}

func schemaRequiredNames(schema map[string]any) []string {
	list, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		if name, ok := entry.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// mergeSchemaProperties merges one schema field's `properties` table across
// an extends chain. A new key from any layer is added; an existing key keeps
// the first definition it was given (lang's extends validation has already
// confirmed every redeclaration agrees with it apart from `default`) and
// gains whichever layer's `default` was added, since at most one layer in a
// valid chain ever adds one.
func mergeSchemaProperties(layers []TaskDocument, field func(TaskDocument) map[string]any) map[string]any {
	properties := map[string]any{}
	for _, layer := range layers {
		schema := field(layer)
		if schema == nil {
			continue
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range sortedKeys(props) {
			prop, ok := props[key].(map[string]any)
			if !ok {
				properties[key] = props[key]
				continue
			}
			existing, has := properties[key].(map[string]any)
			if !has {
				properties[key] = prop
				continue
			}
			def, hasDefault := prop["default"]
			if !hasDefault {
				continue
			}
			widened := make(map[string]any, len(existing)+1)
			for k, v := range existing {
				widened[k] = v
			}
			widened["default"] = def
			properties[key] = widened
		}
	}
	return properties
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
