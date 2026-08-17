package config

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// terminalSchemaJSON is the canonical [terminal] table schema
// (schema/terminal.schema.json). TerminalConfig.Validate is still the
// actual runtime enforcement (it runs directly against the TOML-decoded
// struct, no JSON round-trip needed); this schema is an independently
// authored source of truth TestTerminalConfigValidate_ConformsToCanonicalSchema
// asserts the loader stays in conformance with, so the two can't silently
// drift apart.
//
//go:embed schema/terminal.schema.json
var terminalSchemaJSON []byte

var terminalSchema = mustCompileTerminalSchema()

func mustCompileTerminalSchema() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(terminalSchemaJSON))
	if err != nil {
		panic(fmt.Sprintf("parse schema/terminal.schema.json: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("plect:config:terminal", doc); err != nil {
		panic(fmt.Sprintf("add schema/terminal.schema.json: %v", err))
	}
	schema, err := compiler.Compile("plect:config:terminal")
	if err != nil {
		panic(fmt.Sprintf("compile schema/terminal.schema.json: %v", err))
	}
	return schema
}
