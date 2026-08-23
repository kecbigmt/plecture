package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// discoveredLayer is one cascade layer read as a definition root: every
// declaration under it, whatever directory the author filed it in. A
// directory under a definition root is organization, not identity, so which
// kind a declaration is comes from its own `kind` field rather than from the
// folder it sits in.
type discoveredLayer struct {
	layer layerDir
	defs  []*lang.Definition
}

// A layer's trust decides what it may declare, and the three answers are not
// the same shape. A kind a layer may declare is loaded; a kind it must not
// declare is a load error, because reading it as absent would leave the author
// believing their declaration is live; and a kind that simply does not reach
// this layer is skipped, because a project's own directory holding one is not
// a mistake — it just does not participate.
//
// A workspace provider, a resource observer and a channel are trusted-base
// content: they decide how a session's identity is resolved and how a process
// is reached, so an ancestor overlay declaring one does not participate rather
// than being refused. Effects and task documents reach an ancestor overlay,
// since a project may add its own lifecycle and its own work — but never the
// workspace directory, which is cloned content: shell there, or a declaration
// of the work the clone is about, is refused outright.
var (
	loadedKinds = map[layerScope]map[lang.Kind]bool{
		layerScopeTrusted: {
			lang.KindWorkspaceProvider: true,
			lang.KindResourceObserver:  true,
			lang.KindChannel:           true,
			lang.KindEffect:            true,
			lang.KindTask:              true,
			lang.KindWorkflow:          true,
		},
		layerScopeOverlay: {
			lang.KindEffect:   true,
			lang.KindTask:     true,
			lang.KindWorkflow: true,
		},
		layerScopeWorkspaceDir: {
			lang.KindWorkflow: true,
		},
	}
	refusedKinds = map[layerScope]map[lang.Kind]string{
		layerScopeWorkspaceDir: {
			lang.KindEffect: "clone content must not carry shell",
			lang.KindTask:   "clone content must not declare the work it is about",
		},
	}
)

// layerScope names how far a layer's trust reaches, which is what decides
// which kinds it may declare.
type layerScope int

const (
	// layerScopeTrusted is a plugin's own config root or the machine's global
	// config: machine-owned, so anything may be declared there.
	layerScopeTrusted layerScope = iota
	// layerScopeOverlay is an ancestor `.plect/` directory above the
	// workspace directory: trusted, but a project's own.
	layerScopeOverlay
	// layerScopeWorkspaceDir is the `.plect/` directory inside the workspace
	// directory: cloned, untrusted content.
	layerScopeWorkspaceDir
)

func (l layerDir) scope() layerScope {
	switch {
	case l.workspaceDir:
		return layerScopeWorkspaceDir
	case l.plugin || l.global:
		return layerScopeTrusted
	default:
		return layerScopeOverlay
	}
}

// discoverLayers reads every cascade layer's definition root once, in
// shallowest-first order. workspaceDirPath selects the ancestor overlays; an
// empty one means the trusted base layers alone, which is what a caller
// outside any workspace directory sees.
func (c *Config) discoverLayers(workspaceDirPath string) ([]discoveredLayer, error) {
	out := make([]discoveredLayer, 0, len(c.PluginDirs)+2)
	for _, layer := range c.definitionRoots(workspaceDirPath) {
		defs, err := discoverLayer(layer)
		if err != nil {
			return nil, err
		}
		if len(defs) == 0 {
			continue
		}
		out = append(out, discoveredLayer{layer: layer, defs: defs})
	}
	return out, nil
}

func discoverLayer(layer layerDir) ([]*lang.Definition, error) {
	if _, err := os.Stat(layer.dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := checkRetiredChainsDir(layer.dir); err != nil {
		return nil, err
	}
	discovered, err := lang.DiscoverRoot(layer.dir, lang.ReservedFileNames)
	if err != nil {
		return nil, err
	}
	scope := layer.scope()
	loaded := loadedKinds[scope]
	refused := refusedKinds[scope]
	var defs []*lang.Definition
	for _, def := range discovered {
		if because, no := refused[def.Kind]; no {
			return nil, fmt.Errorf("%s: %q declares kind %q, which a `.plect/` directory inside the workspace directory may not (%s); move it to the global layer, a plugin, or an overlay above the workspace dir", def.File, def.ID, def.Kind, because)
		}
		if !loaded[def.Kind] {
			continue
		}
		if scope == layerScopeWorkspaceDir && filepath.Dir(def.File) != filepath.Join(layer.dir, "workflows") {
			// The workspace-dir layer is not a definition root: only
			// `.plect/workflows/` is read there, so the directory is the
			// allowlist rather than author organization.
			continue
		}
		defs = append(defs, def)
	}
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
	return defs, nil
}

// ofKind selects one kind's declarations from a discovered layer, preserving
// the layer's own id order so a diagnostic naming the first offender is
// reproducible.
func (d discoveredLayer) ofKind(kind lang.Kind) []*lang.Definition {
	var out []*lang.Definition
	for _, def := range d.defs {
		if def.Kind == kind {
			out = append(out, def)
		}
	}
	return out
}

// checkRetiredChainsDir names the one leftover a reader would otherwise meet
// as an unexplained "not a definition table": a definition root admits no
// non-definition TOML, so a surviving `chains/*.toml` from before chains moved
// into the declaration that fires them stops the whole layer from loading.
// Reported here, with the fix, rather than left to the generic diagnostic.
func checkRetiredChainsDir(root string) error {
	entries, err := os.ReadDir(filepath.Join(root, "chains"))
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		return fmt.Errorf("%s is not a definition: declare [[chains]] inside the task document whose instances it fires against, and delete the retired chains/ directory", filepath.Join(root, "chains", entry.Name()))
	}
	return nil
}
