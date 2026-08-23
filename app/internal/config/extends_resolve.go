package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// composeExtendedTaskDocuments materializes the effective declaration for
// every document that declares extends, once ValidateTaskDocuments has
// already confirmed the whole registry composes validly (chain resolution,
// judge-id uniqueness, and the schema key rules). It resolves the same chain
// lang already walked rather than re-deriving those rules, so there is one
// authority for what an extends chain means.
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
		layers := make([]TaskDocument, 0, len(chainDefs)+1)
		for _, def := range chainDefs {
			layerAddress, ok := byDef[def]
			if !ok {
				return fmt.Errorf("task %s: extends chain reaches a definition outside the loaded task documents", address)
			}
			layers = append(layers, original[layerAddress])
		}
		layers = append(layers, doc)
		docs[address] = composeTaskDocument(layers)
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
// across an extends chain. The object's own shape (type, required,
// additionalProperties, ...) comes from the layer nearest the extension,
// outermost first, that declares this field at all; `properties` is replaced
// by the merge across every layer.
func mergeSchemaField(layers []TaskDocument, field func(TaskDocument) map[string]any) map[string]any {
	var base map[string]any
	for i := len(layers) - 1; i >= 0; i-- {
		if schema := field(layers[i]); schema != nil {
			base = schema
			break
		}
	}
	if base == nil {
		return nil
	}
	merged := make(map[string]any, len(base))
	for k, v := range base {
		merged[k] = v
	}
	merged["properties"] = mergeSchemaProperties(layers, field)
	return merged
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
