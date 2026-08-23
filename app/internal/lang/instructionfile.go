package lang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveTaskInstruction fills in a task definition's Instruction from
// whichever of `instruction` (inline) and `instruction_file` (a sidecar
// Markdown asset, relative to the declaring TOML file) it declares. Declaring
// neither leaves Instruction empty; declaring both is ambiguous and rejected
// before either is read.
//
// root is the trusted definition root def.File was discovered under. A
// sidecar has to resolve inside it: the merge algebra treats the body as an
// atomic scalar that happens to live in an adjacent carrier file, and a file
// outside the layer is not that file's own carrier.
func resolveTaskInstruction(def *Definition, root string) error {
	pos := childPos(Position{File: def.File, Path: def.ID}, "instruction")
	_, hasInline := def.Body["instruction"]
	fileRaw, hasFile := def.Body["instruction_file"]
	if hasInline && hasFile {
		return newDiag(CodeTaskInstructionAndFile, LayerStructural, pos,
			"a task declaring both instruction and instruction_file has no unambiguous body")
	}
	if hasInline {
		s, ok := def.Body["instruction"].(string)
		if !ok {
			return newDiag(CodeFieldType, LayerStructural, pos, "`instruction` is a string")
		}
		def.Instruction = s
		return nil
	}
	if !hasFile {
		return nil
	}
	filePos := childPos(Position{File: def.File, Path: def.ID}, "instruction_file")
	relFile, ok := fileRaw.(string)
	if !ok {
		return newDiag(CodeFieldType, LayerStructural, filePos, "`instruction_file` is a string")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absFile, err := filepath.Abs(filepath.Join(filepath.Dir(def.File), relFile))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return newDiag(CodeTaskInstructionFileCrossLayer, LayerSemantic, filePos,
			fmt.Sprintf("instruction_file %q resolves outside this layer", relFile))
	}
	data, err := os.ReadFile(absFile)
	if err != nil {
		return newDiag(CodeTaskInstructionFileMissing, LayerSemantic, filePos,
			fmt.Sprintf("instruction_file %q: %s", relFile, err))
	}
	def.Instruction = string(data)
	return nil
}
