package lang

import (
	"fmt"
	"strings"
)

// PluginExecutables names the executables one enabled, catalog-qualified
// plugin declares in its manifest.
type PluginExecutables struct {
	Alias string
	Path  string
	Names []string
}

// ExecutableRegistry resolves a bin reference to a declared executable.
// Executable lookup is a separate namespace from definition references and
// keeps the slash grammar, because it must split an arbitrary-depth plugin
// path from an executable name.
type ExecutableRegistry struct {
	plugins map[string]map[string]bool // "<alias>/<path>" -> name -> declared
}

// NewExecutableRegistry builds a registry from the enabled plugins'
// manifests. A registry with no plugins declares no executable, which is
// what makes every bin reference under it PLECTURE-CFG-BIN-UNKNOWN.
func NewExecutableRegistry(plugins ...PluginExecutables) *ExecutableRegistry {
	r := &ExecutableRegistry{plugins: make(map[string]map[string]bool, len(plugins))}
	for _, p := range plugins {
		names := make(map[string]bool, len(p.Names))
		for _, n := range p.Names {
			names[n] = true
		}
		r.plugins[p.Alias+"/"+p.Path] = names
	}
	return r
}

// ResolveBin resolves ref from the layer that wrote it. The two ownerships
// accept different grammars, because a catalog alias is user-local and a
// plugin cannot know its own.
func (r *ExecutableRegistry) ResolveBin(ref string, from Ownership) (string, error) {
	pos := Position{Path: ref}
	if from.IsPlugin {
		if strings.Contains(ref, "/") {
			return "", newDiag(CodeRefCrossPlugin, LayerSemantic, pos,
				fmt.Sprintf("shipped plugin config cannot reference another plugin's executable; %q is not a bare name", ref))
		}
		if r.plugins[from.Alias+"/"+from.Path][ref] {
			return ref, nil
		}
		return "", newDiag(CodeBinUnknown, LayerSemantic, pos,
			fmt.Sprintf("%q is not an executable this plugin declares", ref))
	}

	segments := strings.Split(ref, "/")
	if len(segments) < 2 {
		return "", newDiag(CodeRefAliasRequired, LayerSemantic, pos,
			fmt.Sprintf("%q omits its catalog alias; user-authored config names an executable as <catalog-alias>/<plugin-path>[/<executable-name>]", ref))
	}
	sole, soleOK := r.soleExecutable(segments[0], strings.Join(segments[1:], "."))
	named := segments[len(segments)-1]
	namedOK := r.plugins[segments[0]+"/"+strings.Join(segments[1:len(segments)-1], ".")][named]
	switch {
	case soleOK && namedOK:
		return "", fmt.Errorf("%s: %q reads both as a plugin path with one executable and as a plugin path plus an executable name", pos, ref)
	case soleOK:
		return sole, nil
	case namedOK:
		return named, nil
	default:
		return "", newDiag(CodeBinUnknown, LayerSemantic, pos,
			fmt.Sprintf("%q resolves to no declared executable", ref))
	}
}

// soleExecutable reports the one executable a plugin declares, which is what
// lets a reference name the plugin alone.
func (r *ExecutableRegistry) soleExecutable(alias, path string) (string, bool) {
	names := r.plugins[alias+"/"+path]
	if len(names) != 1 {
		return "", false
	}
	for name := range names {
		return name, true
	}
	return "", false
}
