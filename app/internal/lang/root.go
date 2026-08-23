package lang

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverRoot reads every definition in a trusted definition root: a
// plugin's config/ directory, or the user config home excluding reserved
// root files. Every .toml file that is not a reserved root file is read in
// lexicographic order by slash-separated relative path (filepath.WalkDir
// visits entries in that order already). A .md file is never itself a
// definition — it is either a task declaration's instruction sidecar,
// resolved below, or a template asset the language does not read. reserved
// is typically nil for a plugin root (which never contains
// config.toml/catalogs.toml/plect.lock) and ReservedFileNames for the user
// config home.
//
// A reserved name is reserved at the root and nowhere below it: a filename
// carries no meaning inside a root, so skipping a nested file for its
// basename alone would drop a declaration an author had every right to file
// there.
func DiscoverRoot(root string, reserved map[string]bool) ([]*Definition, error) {
	var defs []*Definition
	seen := map[string]*Definition{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return nil
		}
		if reserved[d.Name()] && filepath.Dir(path) == root {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fileDefs, err := ParseDefinitionDocument(path, src)
		if err != nil {
			return err
		}
		for _, def := range fileDefs {
			if def.Kind == KindTask {
				if err := resolveTaskInstruction(def, root); err != nil {
					return err
				}
			}
			if prior, dup := seen[def.ID]; dup {
				return newDiag(CodeIDDuplicate, LayerSemantic, Position{File: path, Path: def.ID},
					"id "+def.ID+" is already declared in "+prior.File)
			}
			seen[def.ID] = def
			defs = append(defs, def)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return defs, nil
}
