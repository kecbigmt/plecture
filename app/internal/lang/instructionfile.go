package lang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveTaskInstructions fills in a task definition's Instruction by
// resolving its `[[<id>.instructions]]` array, in declaration order, and
// joining each element's resolved text with a blank line. An empty or absent
// array leaves Instruction empty.
//
// Each element carries exactly one of `text` (inline) or `file` (a sidecar
// Markdown asset, relative to the declaring TOML file). `file` has to
// resolve inside root, the trusted definition root def.File was discovered
// under: the merge algebra treats each element's text as an atomic scalar
// that happens to live in an adjacent carrier file, and a file outside the
// layer is not that file's own carrier. Resolution follows symlinks to their
// real target before checking containment, so a symlink planted inside the
// layer cannot smuggle a read from outside it.
func resolveTaskInstructions(def *Definition, root string) error {
	pos := Position{File: def.File, Path: def.ID}
	raw, ok := def.Body["instructions"]
	if !ok {
		return nil
	}
	elements, ok := asTableArray(raw)
	if !ok {
		return newDiag(CodeFieldType, LayerStructural, childPos(pos, "instructions"),
			"`instructions` is an array of tables")
	}
	texts := make([]string, 0, len(elements))
	for i, el := range elements {
		at := childPos(childPos(pos, "instructions"), fmt.Sprintf("[%d]", i))
		text, err := resolveInstructionElement(el, at, def.File, root)
		if err != nil {
			return err
		}
		texts = append(texts, text)
	}
	def.Instruction = strings.Join(texts, "\n\n")
	return nil
}

// resolveInstructionElement resolves one `[[instructions]]` entry. Exactly
// one of `text` and `file` must be present — zero is as ambiguous as two,
// since neither says what this element's text is.
func resolveInstructionElement(el map[string]any, pos Position, declaringFile, root string) (string, error) {
	for k := range el {
		if k != "text" && k != "file" {
			return "", newDiag(CodeFieldUnknown, LayerStructural, childPos(pos, k),
				fmt.Sprintf("%q is not part of an instructions element", k))
		}
	}
	textRaw, hasText := el["text"]
	fileRaw, hasFile := el["file"]
	if hasText == hasFile {
		return "", newDiag(CodeTaskInstructionElement, LayerStructural, pos,
			"an instructions element declares exactly one of text and file")
	}
	if hasText {
		s, ok := textRaw.(string)
		if !ok {
			return "", newDiag(CodeFieldType, LayerStructural, childPos(pos, "text"), "`text` is a string")
		}
		return s, nil
	}
	relFile, ok := fileRaw.(string)
	if !ok {
		return "", newDiag(CodeFieldType, LayerStructural, childPos(pos, "file"), "`file` is a string")
	}
	return readInstructionFile(childPos(pos, "file"), declaringFile, root, relFile)
}

func readInstructionFile(pos Position, declaringFile, root, relFile string) (string, error) {
	lexical := filepath.Join(filepath.Dir(declaringFile), relFile)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot := resolveReal(absRoot)
	realFile := resolveReal(lexical)
	rel, err := filepath.Rel(realRoot, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", newDiag(CodeTaskInstructionFileCrossLayer, LayerSemantic, pos,
			fmt.Sprintf("file %q resolves outside this layer", relFile))
	}
	data, err := os.ReadFile(realFile)
	if err != nil {
		return "", newDiag(CodeTaskInstructionFileMissing, LayerSemantic, pos,
			fmt.Sprintf("file %q: %s", relFile, err))
	}
	return string(data), nil
}

// resolveReal returns path's real location, following every symlink a
// component along it may be — including one whose final component does not
// exist, which os.ReadFile still has to fail on its own terms, and including
// one whose parent directory does not exist either, where a symlink is not
// in question and the lexical path is the only answer available.
func resolveReal(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	dir := filepath.Dir(path)
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(realDir, filepath.Base(path))
	}
	return path
}
