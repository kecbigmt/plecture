package plugins

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
	"text/template/parse"
)

// binCandidate is one way ref could be decomposed into a mounted plugin plus
// an executable inside it.
type binCandidate struct {
	mount Mounted
	exec  Executable
}

// ResolveBin resolves a `{{bin ref}}` reference against mounted (the
// catalog-qualified plugins folded into the current config, in declaration
// order) to an absolute executable path. sourcePath is the file the
// referencing template came from — the catalog-load worktree's absolute
// path — used only to find ref's containing plugin for the bare-name
// reading below; pass "" when the template has no file origin (e.g. it was
// built in-process by a test).
//
// ref has two readings:
//
//   - A bare executable name (no "/"): resolves against the *containing*
//     plugin only — the plugin sourcePath was mounted from — matched
//     against that plugin's own `[[executables]]`. Shipped config never
//     knows the alias its catalog was registered under, so this is the
//     only form plugin-mounted content may use. It is unavailable when
//     sourcePath resolves to no mounted plugin (hand-authored config
//     outside any plugin has no "containing plugin" to resolve against).
//   - "<catalog-alias>/<plugin-path>[/<executable-name>]": the
//     fully-qualified form, for user-authored config referencing any
//     mounted plugin (its own or another's) by the alias the user chose at
//     registration. Two sub-readings are tried against every mounted
//     plugin:
//   - ref exactly equals the plugin's id: valid only when that plugin
//     declares exactly one executable (the terse common case).
//   - ref is "<plugin-id>/<executable-name>" for some mounted plugin id
//     and one of its declared executable names.
//     Both sub-readings can match for every mounted plugin whose id is a
//     prefix of ref, so nested plugin paths can collide: a reference is a
//     load error when more than one candidate resolution exists, because no
//     slash-based syntax can disambiguate that collision under
//     arbitrary-depth plugin paths — the design deliberately refuses to
//     guess.
func ResolveBin(mounted []Mounted, sourcePath, ref string) (string, error) {
	if ref != "" && !strings.Contains(ref, "/") {
		return resolvePluginLocalBin(mounted, sourcePath, ref)
	}

	var candidates []binCandidate
	for _, m := range mounted {
		switch {
		case ref == m.ID:
			if len(m.Manifest.Executables) == 1 {
				candidates = append(candidates, binCandidate{mount: m, exec: m.Manifest.Executables[0]})
			}
		case strings.HasPrefix(ref, m.ID+"/"):
			execName := strings.TrimPrefix(ref, m.ID+"/")
			for _, ex := range m.Manifest.Executables {
				if ex.Name == execName {
					candidates = append(candidates, binCandidate{mount: m, exec: ex})
				}
			}
		}
	}

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf(`{{bin %q}}: no mounted plugin resolves this reference (want "<catalog-alias>/<plugin-path>" or "<catalog-alias>/<plugin-path>/<executable-name>")`, ref)
	case 1:
		return filepath.Join(candidates[0].mount.Dir, candidates[0].exec.Path), nil
	default:
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c.mount.ID + " (" + c.exec.Name + ")"
		}
		return "", fmt.Errorf("{{bin %q}}: ambiguous; matches more than one plugin/executable reading: %v", ref, ids)
	}
}

// resolvePluginLocalBin resolves a bare executable name against the single
// plugin that mounted sourcePath, by name only (no alias, no path) — the
// containing plugin's own [[executables]] never has a duplicate name
// (LoadManifest rejects that), so at most one match can ever exist.
func resolvePluginLocalBin(mounted []Mounted, sourcePath, ref string) (string, error) {
	owner, ok := containingPlugin(mounted, sourcePath)
	if !ok {
		return "", fmt.Errorf(`{{bin %q}}: a bare executable name only resolves inside plugin-mounted config; this file was not mounted from any catalog plugin (use "<catalog-alias>/<plugin-path>/<executable-name>" instead)`, ref)
	}
	for _, ex := range owner.Manifest.Executables {
		if ex.Name == ref {
			return filepath.Join(owner.Dir, ex.Path), nil
		}
	}
	return "", fmt.Errorf("{{bin %q}}: plugin %q declares no executable named %q", ref, owner.ID, ref)
}

// containingPlugin finds the mounted plugin whose directory contains
// sourcePath, i.e. the plugin sourcePath's file was mounted from. Picks the
// longest matching Dir so a plugin nested under another plugin's directory
// resolves to its own, more specific, manifest rather than the outer one's.
func containingPlugin(mounted []Mounted, sourcePath string) (Mounted, bool) {
	var best Mounted
	found := false
	for _, m := range mounted {
		if m.Dir == "" {
			continue
		}
		if !strings.HasPrefix(sourcePath, m.Dir+string(filepath.Separator)) {
			continue
		}
		if !found || len(m.Dir) > len(best.Dir) {
			best = m
			found = true
		}
	}
	return best, found
}

// binScanFuncs stubs every template function a hook string calling `{{bin
// ...}}` might also use, so BinRefs's parse-only pass succeeds regardless of
// what those other functions actually do — it never executes the template,
// only walks its parse tree. Kept in sync with the real function set task
// hook rendering registers (app/internal/task's templateFuncs plus each
// render call's own dynamicFuncs); a hook string calling a function outside
// this set fails to parse here and BinRefs reports zero references for it
// rather than erroring, so a template function added later without a
// matching entry here degrades this scan silently instead of breaking config
// load for every hook that happens to use it.
var binScanFuncs = template.FuncMap{
	"bin":        func(args ...any) (any, error) { return nil, nil },
	"get":        func(args ...any) any { return nil },
	"shellQuote": func(args ...any) any { return nil },
	"json":       func(args ...any) (any, error) { return nil, nil },
}

// BinRefs statically scans tmplStr's parse tree for `{{bin "<ref>"}}` calls
// (bare or piped, e.g. `{{bin "x" | shellQuote}}`) and returns each ref
// string literal found, in source order. It never renders the template, so
// a reference is reported even when the value it would resolve to depends on
// data this scan has no access to. Parse failure (unknown function, syntax
// error) is reported as (nil, nil), not an error: this scan is a best-effort
// load-time diagnostic layered on top of the render-time renderer, which
// remains the authority on whether a template is actually valid — see
// binScanFuncs.
func BinRefs(tmplStr string) []string {
	t, err := template.New("bin-scan").Funcs(binScanFuncs).Parse(tmplStr)
	if err != nil {
		return nil
	}
	var out []string
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.IfNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.List)
			walk(x.ElseList)
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x == nil {
				return
			}
			for _, c := range x.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			if len(x.Args) >= 2 {
				if id, ok := x.Args[0].(*parse.IdentifierNode); ok && id.Ident == "bin" {
					if s, ok := x.Args[1].(*parse.StringNode); ok {
						out = append(out, s.Text)
					}
				}
			}
		}
	}
	walk(t.Root)
	return out
}
