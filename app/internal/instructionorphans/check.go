// Package instructionorphans finds a Markdown file under a definition root
// that no task's `instructions` element names by its `file`. It is the
// reverse of the load-time check that such a file naming no file is a load
// error (PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING): a sidecar nothing
// points at has no load error to surface it, so it silently rots instead.
//
// It decodes TOML rather than scanning source text, because the two things a
// text scan gets wrong — a commented-out reference and a differently-quoted
// key — are exactly what a real parser does not get wrong.
package instructionorphans

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// Check walks each root and returns one description per orphaned Markdown
// file, sorted. A root that does not exist is skipped, not an error.
func Check(roots []string) ([]string, error) {
	var orphans []string
	for _, root := range roots {
		found, err := checkRoot(root)
		if err != nil {
			return nil, err
		}
		orphans = append(orphans, found...)
	}
	sort.Strings(orphans)
	return orphans, nil
}

func checkRoot(root string) ([]string, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}

	referenced := map[string]bool{}
	var mdFiles []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".toml":
			files, err := taskInstructionFiles(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			for _, f := range files {
				referenced[filepath.Join(filepath.Dir(path), f)] = true
			}
		case ".md":
			if d.Name() != "README.md" {
				mdFiles = append(mdFiles, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var orphans []string
	for _, md := range mdFiles {
		if !referenced[md] {
			orphans = append(orphans, fmt.Sprintf("%s (not named by any instructions element's file under %s)", md, root))
		}
	}
	return orphans, nil
}

// taskInstructionFiles reads every `file` value a `kind = "task"` table's
// `instructions` array declares. It decodes structurally rather than through
// the full definition loader: a corpus fixture may be deliberately invalid
// for a rule this check has no stake in (an element declaring both text and
// file, say), and a decode failure is the loader's own concern to report,
// not this one's — a file this check cannot read as TOML contributes no
// references and is otherwise silently skipped.
func taskInstructionFiles(path string) ([]string, error) {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, nil
	}
	var files []string
	for _, v := range raw {
		tbl, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := tbl["kind"].(string); kind != "task" {
			continue
		}
		instructions, _ := tbl["instructions"].([]any)
		for _, el := range instructions {
			m, ok := el.(map[string]any)
			if !ok {
				continue
			}
			if f, ok := m["file"].(string); ok {
				files = append(files, f)
			}
		}
	}
	return files, nil
}
