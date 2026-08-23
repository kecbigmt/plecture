package lang

import (
	"fmt"
	"strings"
)

// Ownership names the layer a reference is written from: a plugin's own
// definitions (which may only use the relative form), or the user-owned
// layer stack (which may use either form, but must qualify catalog content
// with its alias).
type Ownership struct {
	IsPlugin bool
	Alias    string // this plugin's own enabling alias; empty for a user-owned Ownership
	Path     string // this plugin's own dotted path; empty for a user-owned Ownership
}

// PluginLayer is one enabled, catalog-qualified plugin's own namespace.
type PluginLayer struct {
	Alias string
	Path  string
	Defs  []*Definition
}

// Registry resolves references against every layer a config tree combines:
// each enabled plugin's own namespace, plus the (already layer-merged)
// user-owned namespace.
type Registry struct {
	plugins map[string]map[string]*Definition // "<alias>.<path>" -> id -> Definition
	user    map[string]*Definition
	// owner records which layer wrote each definition this registry holds, so
	// a caller resolving a second reference hop — extends walking a base's own
	// extends, for instance — resolves it in that base's namespace rather than
	// the first reference's.
	owner map[*Definition]Ownership
}

// NewRegistry builds a Registry from the enabled plugin layers and the
// merged user-owned namespace (the output of repeated MergeLayer calls).
func NewRegistry(plugins []PluginLayer, user []*Definition) *Registry {
	r := &Registry{
		plugins: make(map[string]map[string]*Definition, len(plugins)),
		user:    make(map[string]*Definition, len(user)),
		owner:   make(map[*Definition]Ownership),
	}
	for _, p := range plugins {
		layer := make(map[string]*Definition, len(p.Defs))
		from := Ownership{IsPlugin: true, Alias: p.Alias, Path: p.Path}
		for _, d := range p.Defs {
			layer[d.ID] = d
			r.owner[d] = from
		}
		r.plugins[p.Alias+"."+p.Path] = layer
	}
	for _, d := range user {
		r.user[d.ID] = d
		r.owner[d] = Ownership{}
	}
	return r
}

// OwnerOf reports the Ownership def was registered under, for resolving a
// reference written inside def itself. A def this registry did not load
// resolves as user-owned, the safe default for a definition reached only
// through a value it does not itself control (a Resolve result is always
// registered, so this only matters for a def a caller constructed outside
// the registry).
func (r *Registry) OwnerOf(def *Definition) Ownership {
	return r.owner[def]
}

// existsUnderSomeEnabledPlugin reports whether id is declared by any
// registered plugin layer — what distinguishes PLECTURE-CFG-REF-ALIAS-
// REQUIRED (the id exists, just unqualified) from PLECTURE-CFG-UNKNOWN-REF
// (it does not exist anywhere this registry knows about).
func (r *Registry) existsUnderSomeEnabledPlugin(id string) bool {
	for _, layer := range r.plugins {
		if _, ok := layer[id]; ok {
			return true
		}
	}
	return false
}

// Resolve looks up ref (a bare id, or a catalog-qualified dotted address)
// from the given Ownership, applying declarations.md's References rules.
func (r *Registry) Resolve(ref string, from Ownership) (*Definition, error) {
	parts := strings.Split(ref, ".")

	if from.IsPlugin {
		if len(parts) > 1 {
			return nil, newDiag(CodeRefCrossPlugin, LayerSemantic, Position{Path: ref},
				fmt.Sprintf("a plugin-owned reference must be relative; %q names a catalog alias or another plugin's ownership segment", ref))
		}
		layer := r.plugins[from.Alias+"."+from.Path]
		if def, ok := layer[ref]; ok {
			return def, nil
		}
		return nil, newDiag(CodeUnknownRef, LayerSemantic, Position{Path: ref},
			fmt.Sprintf("%q resolves to no definition in this plugin", ref))
	}

	if len(parts) == 1 {
		if def, ok := r.user[ref]; ok {
			return def, nil
		}
		if r.existsUnderSomeEnabledPlugin(ref) {
			return nil, newDiag(CodeRefAliasRequired, LayerSemantic, Position{Path: ref},
				fmt.Sprintf("%q is not user-owned; a user-owned reference to catalog content must carry its catalog alias", ref))
		}
		return nil, newDiag(CodeUnknownRef, LayerSemantic, Position{Path: ref},
			fmt.Sprintf("%q resolves to no definition", ref))
	}

	alias := parts[0]
	id := parts[len(parts)-1]
	pluginPath := strings.Join(parts[1:len(parts)-1], ".")
	layer, ok := r.plugins[alias+"."+pluginPath]
	if !ok {
		return nil, newDiag(CodeUnknownRef, LayerSemantic, Position{Path: ref},
			fmt.Sprintf("%q names no enabled plugin at %s.%s", ref, alias, pluginPath))
	}
	if def, ok := layer[id]; ok {
		return def, nil
	}
	return nil, newDiag(CodeUnknownRef, LayerSemantic, Position{Path: ref},
		fmt.Sprintf("%q resolves to no definition in %s.%s", ref, alias, pluginPath))
}

// ExpectKind resolves ref and checks its declared kind against want, naming
// site in the diagnostic when it does not match.
func (r *Registry) ExpectKind(ref string, from Ownership, want Kind, site string) (*Definition, error) {
	def, err := r.Resolve(ref, from)
	if err != nil {
		return nil, err
	}
	if def.Kind != want {
		return nil, newDiag(CodeKindMismatch, LayerSemantic, Position{Path: site},
			fmt.Sprintf("%s expects kind %q; %q resolves to kind %q", site, want, ref, def.Kind))
	}
	return def, nil
}

// staticRef reads a field expected to hold a plain reference string. A
// tagged value (a table, e.g. `{ from = ... }`) there is
// PLECTURE-CFG-REF-DYNAMIC: static topology must stay statically
// discoverable.
func staticRef(v any, site string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", newDiag(CodeRefDynamic, LayerStructural, Position{Path: site},
			fmt.Sprintf("%s must be a static reference, not a computed value", site))
	}
	return s, nil
}

// ResolveWorkflowRefs checks a workflow definition's static references: its
// workspace_provider (kind workspace_provider), each node's uses (kind
// effect), and each event.channel entry's uses (kind channel).
func ResolveWorkflowRefs(def *Definition, from Ownership, r *Registry) error {
	if wp, ok := def.Body["workspace_provider"]; ok {
		ref, err := staticRef(wp, def.ID+".workspace_provider")
		if err != nil {
			return err
		}
		if _, err := r.ExpectKind(ref, from, KindWorkspaceProvider, def.ID+".workspace_provider"); err != nil {
			return err
		}
	}
	if inputsVal, ok := def.Body["workspace_provider_inputs"]; ok {
		inputs, ok := inputsVal.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.workspace_provider_inputs: expected a table", def.ID)
		}
		for key, v := range inputs {
			if _, isTagged := v.(map[string]any); isTagged {
				return newDiag(CodeValueTagSurface, LayerStructural,
					Position{Path: fmt.Sprintf("%s.workspace_provider_inputs.%s", def.ID, key)},
					"a workspace provider's hooks run before any node output exists, so its parameters are literal data")
			}
		}
	}
	if nodesVal, ok := def.Body["nodes"]; ok {
		nodes, ok := asTableArray(nodesVal)
		if !ok {
			return fmt.Errorf("%s.nodes: expected an array of tables", def.ID)
		}
		for i, node := range nodes {
			uses, ok := node["uses"]
			if !ok {
				return fmt.Errorf("%s.nodes[%d]: `uses` is required", def.ID, i)
			}
			site := fmt.Sprintf("%s.nodes[%d].uses", def.ID, i)
			ref, err := staticRef(uses, site)
			if err != nil {
				return err
			}
			if _, err := r.ExpectKind(ref, from, KindEffect, site); err != nil {
				return err
			}
		}
	}
	if eventVal, ok := def.Body["event"]; ok {
		event, ok := eventVal.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.event: expected a table", def.ID)
		}
		channelsVal, ok := event["channel"]
		if !ok {
			return nil
		}
		channels, ok := asTableArray(channelsVal)
		if !ok {
			return fmt.Errorf("%s.event.channel: expected an array of tables", def.ID)
		}
		for i, ch := range channels {
			uses, ok := ch["uses"]
			if !ok {
				return fmt.Errorf("%s.event.channel[%d]: `uses` is required", def.ID, i)
			}
			site := fmt.Sprintf("%s.event.channel[%d].uses", def.ID, i)
			ref, err := staticRef(uses, site)
			if err != nil {
				return err
			}
			if _, err := r.ExpectKind(ref, from, KindChannel, site); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveConfigChannels checks that every config.toml `channels` entry
// resolves to a definition of kind channel — always from user Ownership,
// since config.toml is itself user-owned.
func ResolveConfigChannels(cfg *ConfigToml, r *Registry) error {
	for i, ref := range cfg.Channels {
		site := fmt.Sprintf("channels[%d]", i)
		if _, err := r.ExpectKind(ref, Ownership{}, KindChannel, site); err != nil {
			return err
		}
	}
	return nil
}
