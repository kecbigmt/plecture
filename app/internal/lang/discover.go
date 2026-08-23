package lang

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Kind is one of the definition-block kinds the language vocabulary
// declares.
type Kind string

const (
	KindEffect            Kind = "effect"
	KindTask              Kind = "task"
	KindChannel           Kind = "channel"
	KindWorkflow          Kind = "workflow"
	KindWorkspaceProvider Kind = "workspace_provider"
	KindResourceObserver  Kind = "resource_observer"
)

var validKinds = map[Kind]bool{
	KindEffect:            true,
	KindTask:              true,
	KindChannel:           true,
	KindWorkflow:          true,
	KindWorkspaceProvider: true,
	KindResourceObserver:  true,
}

// idPattern is the definition id grammar: a TOML bare-key segment excluding
// dots (address separators) and hyphens (an effect id doubles as a workflow
// node id, which must be a safe dotted path segment). BurntSushi/toml decodes
// a bare and a quoted key to the same Go map key, so this check cannot tell
// them apart — "quoted keys are not ids" is not enforced here.
var idPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isValidID(id string) bool {
	return idPattern.MatchString(id)
}

// asTableArray normalizes an array-of-tables field to []map[string]any.
// BurntSushi/toml decodes such a field as []map[string]interface{} when
// decoding into a generic map, but a value built by hand (as in a test, or
// after mergeCascade's own append) may already be a plain []interface{}
// holding map[string]any elements; this accepts either.
func asTableArray(v any) ([]map[string]any, bool) {
	switch arr := v.(type) {
	case []map[string]any:
		return arr, true
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	}
	return nil, false
}

// Definition is one discovered `[<id>] kind = "<kind>"` table, whatever file
// it came from. Body holds every field of the table except id and kind, with
// nested tables and arrays exactly as TOML decoded them. Instruction is the
// prose below a task document's closing `+++`, empty for every other kind.
type Definition struct {
	ID          string
	Kind        Kind
	Body        map[string]any
	Instruction string
	File        string
}

// ParseDefinitionDocument decodes a TOML definition document (one that is
// not a reserved root file) into its top-level definitions, applying the
// structural rules that gate whether each block is even a definition:
// `kind` present and in the vocabulary, the id well-formed, and — since a
// kind with a body belongs in a task document's frontmatter, never a TOML
// file — `kind = "task"` is a load error here.
func ParseDefinitionDocument(path string, src []byte) ([]*Definition, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(src), &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s: no top-level definition table", path)
	}
	defs := make([]*Definition, 0, len(raw))
	for id, v := range raw {
		tbl, ok := v.(map[string]any)
		if !ok {
			return nil, newDiag(CodeKindMissing, LayerStructural, Position{File: path, Path: id},
				fmt.Sprintf("%q is not a definition table", id))
		}
		def, err := parseDefinitionTable(path, id, tbl)
		if err != nil {
			return nil, err
		}
		if def.Kind == KindTask {
			return nil, newDiag(CodeTaskInTOMLDocument, LayerStructural, Position{File: path, Path: id + ".kind"},
				fmt.Sprintf("definition %q declares kind = \"task\" in a TOML document; a kind with a body belongs in a task document's frontmatter", id))
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// parseDefinitionTable applies the structural rules common to every
// definition table, regardless of which file kind it was found in: kind
// present and in the vocabulary, then the id well-formed.
func parseDefinitionTable(path, id string, tbl map[string]any) (*Definition, error) {
	kindVal, ok := tbl["kind"]
	if !ok {
		return nil, newDiag(CodeKindMissing, LayerStructural, Position{File: path, Path: id},
			fmt.Sprintf("definition %q declares no kind", id))
	}
	kindStr, ok := kindVal.(string)
	if !ok || !validKinds[Kind(kindStr)] {
		return nil, newDiag(CodeKindUnknown, LayerStructural, Position{File: path, Path: id + ".kind"},
			fmt.Sprintf("definition %q declares kind %v, outside the vocabulary", id, kindVal))
	}
	if !isValidID(id) {
		return nil, newDiag(CodeIDInvalid, LayerStructural, Position{File: path, Path: id},
			fmt.Sprintf("id %q does not match ^[A-Za-z_][A-Za-z0-9_]*$", id))
	}
	body := make(map[string]any, len(tbl))
	for k, v := range tbl {
		if k == "kind" {
			continue
		}
		body[k] = v
	}
	return &Definition{ID: id, Kind: Kind(kindStr), Body: body, File: path}, nil
}

const frontmatterDelim = "+++"

// ParseTaskDocument decodes a task document's `+++`-delimited TOML
// frontmatter into its one definition, carrying the prose below the closing
// delimiter as that definition's instruction.
func ParseTaskDocument(path string, src []byte) (*Definition, error) {
	s := string(src)
	if !strings.HasPrefix(s, frontmatterDelim+"\n") {
		return nil, newDiag(CodeTaskFrontmatterMissing, LayerStructural, Position{File: path},
			"a task document must open with +++ frontmatter")
	}
	rest := s[len(frontmatterDelim)+1:]
	end := strings.Index(rest, "\n"+frontmatterDelim)
	if end < 0 {
		return nil, newDiag(CodeTaskFrontmatterMissing, LayerStructural, Position{File: path},
			"frontmatter is not terminated by a closing +++")
	}
	fm := rest[:end+1]
	instruction := strings.TrimPrefix(rest[end+1+len(frontmatterDelim):], "\n")

	var raw map[string]any
	if _, err := toml.Decode(fm, &raw); err != nil {
		return nil, fmt.Errorf("%s: frontmatter does not parse as TOML: %w", path, err)
	}
	if len(raw) != 1 {
		return nil, newDiag(CodeTaskBlockCount, LayerStructural, Position{File: path},
			fmt.Sprintf("frontmatter holds %d declarations, want exactly 1", len(raw)))
	}
	for id, v := range raw {
		tbl, ok := v.(map[string]any)
		if !ok {
			return nil, newDiag(CodeKindMissing, LayerStructural, Position{File: path, Path: id},
				fmt.Sprintf("%q is not a definition table", id))
		}
		def, err := parseDefinitionTable(path, id, tbl)
		if err != nil {
			return nil, err
		}
		// The mirror of the task-in-TOML rule: a kind with a body lives in a
		// Markdown file and a kind without one in TOML, so a bodiless kind in
		// frontmatter would carry an instruction nothing reads.
		if def.Kind != KindTask {
			return nil, newDiag(CodeBodilessInTaskDocument, LayerStructural, Position{File: path, Path: id + ".kind"},
				fmt.Sprintf("kind %q has no body to carry, so it is declared in a TOML document rather than a task document's frontmatter", def.Kind))
		}
		def.Instruction = instruction
		return def, nil
	}
	panic("unreachable: len(raw) == 1")
}
