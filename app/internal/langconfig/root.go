package langconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverRoot reads every definition in a trusted definition root: a
// plugin's config/ directory, or the user config home excluding reserved
// root files. Every .toml file that is not a reserved root file, and every
// .md file opening with `+++` frontmatter, is read in lexicographic order by
// slash-separated relative path (filepath.WalkDir visits entries in that
// order already). A .md file without that frontmatter is a template asset,
// not a definition, and is skipped. reserved is typically nil for a plugin
// root (which never contains config.toml/catalogs.toml/plect.lock) and
// ReservedFileNames for the user config home.
func DiscoverRoot(root string, reserved map[string]bool) ([]*Definition, error) {
	var defs []*Definition
	seen := map[string]*Definition{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasSuffix(name, ".toml"):
			if reserved[name] {
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
				if prior, dup := seen[def.ID]; dup {
					return newDiag(CodeIDDuplicate, LayerSemantic, Position{File: path, Path: def.ID},
						"id "+def.ID+" is already declared in "+prior.File)
				}
				seen[def.ID] = def
				defs = append(defs, def)
			}
		case strings.HasSuffix(name, ".md"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(string(src), frontmatterDelim+"\n") {
				return nil // a template asset, not a definition
			}
			def, err := ParseTaskDocument(path, src)
			if err != nil {
				return err
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
