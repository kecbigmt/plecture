package task

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// binCandidate is one way ref could be decomposed into a mounted plugin plus
// an executable inside it.
type binCandidate struct {
	mount plugins.Mounted
	exec  plugins.Executable
}

// resolveBin resolves a `{{bin ref}}` reference against mounted (the
// catalog-qualified plugins folded into the current config, in declaration
// order) to an absolute executable path.
//
// ref is always "<catalog-alias>/<plugin-path>[/<executable-name>]": a
// reference with no catalog alias can never match, because plugin paths are
// unique only inside a catalog. Two readings are tried against every
// mounted plugin:
//
//   - ref exactly equals the plugin's id: valid only when that plugin
//     declares exactly one executable (the terse common case).
//   - ref is "<plugin-id>/<executable-name>" for some mounted plugin id and
//     one of its declared executable names.
//
// Both readings can be tried for every mounted plugin whose id is a prefix
// of ref, so nested plugin paths can collide: a reference is a load error
// when more than one candidate resolution exists, because no slash-based
// syntax can disambiguate that collision under arbitrary-depth plugin
// paths — the design deliberately refuses to guess.
func resolveBin(mounted []plugins.Mounted, ref string) (string, error) {
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
