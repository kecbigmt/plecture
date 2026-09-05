package lang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// CompileSchema returns (nil, nil) when neither input is set; both set is an error.
func CompileSchema(inline map[string]any, filePath, inlineID string) (*jsonschema.Schema, error) {
	hasInline := len(inline) > 0
	hasFile := filePath != ""
	if hasInline && hasFile {
		return nil, fmt.Errorf("inline schema and schema file are mutually exclusive")
	}
	switch {
	case hasInline:
		return CompileInlineSchema(inline, inlineID)
	case hasFile:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filePath, err)
		}
		return compileSchemaBytes(filePath, data)
	}
	return nil, nil
}

// CompileInlineSchema compiles a schema known to be declared, including the
// empty schema whose empty map would otherwise be indistinguishable from an
// omitted optional inline schema to CompileSchema.
func CompileInlineSchema(inline map[string]any, inlineID string) (*jsonschema.Schema, error) {
	// TOML decodes ints as int64; round-trip through JSON so the validator sees
	// the same shape it would from a .json file.
	raw, err := json.Marshal(inline)
	if err != nil {
		return nil, fmt.Errorf("marshal inline schema: %w", err)
	}
	return compileSchemaBytes(inlineID, raw)
}

func compileSchemaBytes(id string, raw []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", id, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("add %s: %w", id, err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", id, err)
	}
	return schema, nil
}
