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
	plugins map[string]namespace // "<alias>.<path>" -> namespace
	user    namespace
	// owner records which layer wrote each definition this registry holds, so
	// a caller resolving a second reference hop — extends walking a base's own
	// extends, for instance — resolves it in that base's namespace rather than
	// the first reference's.
	owner map[*Definition]Ownership
}

// namespace is keyed by kind too: one id may name a definition of each kind
// across user-owned layers, and every reference site expects one kind.
type namespace map[namespaceKey]*Definition

type namespaceKey struct {
	kind Kind
	id   string
}

func newNamespace(defs []*Definition) namespace {
	n := make(namespace, len(defs))
	for _, d := range defs {
		n[namespaceKey{kind: d.Kind, id: d.ID}] = d
	}
	return n
}

func (n namespace) lookup(id string, kind Kind) (*Definition, bool) {
	def, ok := n[namespaceKey{kind: kind, id: id}]
	return def, ok
}

// otherKind picks the smallest kind so a diagnostic naming it is reproducible.
func (n namespace) otherKind(id string, want Kind) (*Definition, bool) {
	var found *Definition
	for key, def := range n {
		if key.id != id || key.kind == want {
			continue
		}
		if found == nil || key.kind < found.Kind {
			found = def
		}
	}
	return found, found != nil
}

// NewRegistry builds a Registry from the enabled plugin layers and the
// user-owned namespace after its layers have replaced same-kind declarations.
func NewRegistry(plugins []PluginLayer, user []*Definition) *Registry {
	r := &Registry{
		plugins: make(map[string]namespace, len(plugins)),
		user:    newNamespace(user),
		owner:   make(map[*Definition]Ownership),
	}
	for _, p := range plugins {
		from := Ownership{IsPlugin: true, Alias: p.Alias, Path: p.Path}
		for _, d := range p.Defs {
			r.owner[d] = from
		}
		r.plugins[p.Alias+"."+p.Path] = newNamespace(p.Defs)
	}
	for _, d := range user {
		r.owner[d] = Ownership{}
	}
	return r
}

// OwnerOf reports the Ownership def was registered under, for resolving a
// reference written inside def itself. A def this registry did not load
// resolves as user-owned, the safe default for a definition reached only
// through a value it does not itself control (an ExpectKind result is always
// registered, so this only matters for a def a caller constructed outside
// the registry).
func (r *Registry) OwnerOf(def *Definition) Ownership {
	return r.owner[def]
}

// declaredByAnEnabledPlugin is what tells PLECTURE-CFG-REF-ALIAS-REQUIRED (the
// id exists, just unqualified) apart from an id only the user namespace answers.
func (r *Registry) declaredByAnEnabledPlugin(id string, kinds func(Kind) bool) bool {
	for _, layer := range r.plugins {
		for key := range layer {
			if key.id == id && kinds(key.kind) {
				return true
			}
		}
	}
	return false
}

// ExpectKind resolves ref (a bare id, or a catalog-qualified dotted address)
// to a definition of kind want, applying declarations.md's References rules.
func (r *Registry) ExpectKind(ref string, from Ownership, want Kind, site string) (*Definition, error) {
	parts := strings.Split(ref, ".")
	if from.IsPlugin {
		if len(parts) > 1 {
			return nil, newDiag(CodeRefCrossPlugin, LayerSemantic, Position{Path: ref},
				fmt.Sprintf("a plugin-owned reference must be relative; %q names a catalog alias or another plugin's ownership segment", ref))
		}
		return resolveIn(r.plugins[from.Alias+"."+from.Path], ref, ref, want, site, "in this plugin")
	}
	if len(parts) == 1 {
		if def, ok := r.user.lookup(ref, want); ok {
			return def, nil
		}
		if r.declaredByAnEnabledPlugin(ref, func(k Kind) bool { return k == want }) {
			return nil, aliasRequired(ref)
		}
		if other, ok := r.user.otherKind(ref, want); ok {
			return nil, kindMismatch(ref, other, want, site)
		}
		if r.declaredByAnEnabledPlugin(ref, func(Kind) bool { return true }) {
			return nil, aliasRequired(ref)
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
	return resolveIn(layer, ref, id, want, site, "in "+alias+"."+pluginPath)
}

func resolveIn(layer namespace, ref, id string, want Kind, site, where string) (*Definition, error) {
	if def, ok := layer.lookup(id, want); ok {
		return def, nil
	}
	if other, ok := layer.otherKind(id, want); ok {
		return nil, kindMismatch(ref, other, want, site)
	}
	return nil, newDiag(CodeUnknownRef, LayerSemantic, Position{Path: ref},
		fmt.Sprintf("%q resolves to no definition %s", ref, where))
}

func aliasRequired(ref string) *Diagnostic {
	return newDiag(CodeRefAliasRequired, LayerSemantic, Position{Path: ref},
		fmt.Sprintf("%q is not user-owned; a user-owned reference to catalog content must carry its catalog alias", ref))
}

func kindMismatch(ref string, found *Definition, want Kind, site string) *Diagnostic {
	return newDiag(CodeKindMismatch, LayerSemantic, Position{Path: site},
		fmt.Sprintf("%s expects a definition of kind %q; %q names %s %q", site, want, ref, found.Kind, found.ID))
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
// entry resource (kind resource), each node's uses (kind effect) and each event.channel entry's uses (kind
// channel).
func ResolveWorkflowRefs(def *Definition, from Ownership, r *Registry) error {
	if raw, ok := def.Body["resource"]; ok {
		ref, err := staticRef(raw, def.ID+".resource")
		if err != nil {
			return err
		}
		if _, err := r.ExpectKind(ref, from, KindResource, def.ID+".resource"); err != nil {
			return err
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
