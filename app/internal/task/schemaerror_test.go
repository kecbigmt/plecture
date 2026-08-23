package task

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const enumInputsSchema = `{
	"type": "object",
	"additionalProperties": false,
	"required": ["task"],
	"properties": {
		"task": {"type": "string", "enum": ["work", "review", "none"]}
	}
}`

func mustCompile(t *testing.T, schemaJSON string) *jsonschema.Schema {
	t.Helper()
	var inline map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &inline); err != nil {
		t.Fatalf("unmarshal schema fixture: %v", err)
	}
	sch, err := lang.CompileSchema(inline, "", "mem://test")
	if err != nil {
		t.Fatalf("CompileSchema: %v", err)
	}
	return sch
}

// DescribeValidationError is generic over any inputs_schema: it derives the
// hint purely from what the schema declares (an enum on the offending
// property), never from a hardcoded property name.
func TestDescribeValidationError_MissingRequiredWithEnum(t *testing.T) {
	sch := mustCompile(t, enumInputsSchema)
	var value map[string]any
	_ = json.Unmarshal([]byte(`{}`), &value)

	err := sch.Validate(value)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := DescribeValidationError(sch, err)
	if !strings.Contains(msg, "missing property") {
		t.Errorf("message lost the underlying error: %q", msg)
	}
	if !strings.Contains(msg, `"task": valid choices are work, review, none`) {
		t.Errorf("message missing enum hint: %q", msg)
	}
}

func TestDescribeValidationError_EnumMismatch(t *testing.T) {
	sch := mustCompile(t, enumInputsSchema)
	var value map[string]any
	_ = json.Unmarshal([]byte(`{"task":"wrok"}`), &value)

	err := sch.Validate(value)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := DescribeValidationError(sch, err)
	if !strings.Contains(msg, `"task": valid choices are work, review, none`) {
		t.Errorf("message missing enum hint: %q", msg)
	}
}

// A property without an enum gets no hint — the decoration must not invent
// choices that the schema never declared.
func TestDescribeValidationError_NoEnumNoHint(t *testing.T) {
	sch := mustCompile(t, `{
		"type": "object",
		"required": ["name"],
		"properties": {"name": {"type": "string"}}
	}`)
	var value map[string]any
	_ = json.Unmarshal([]byte(`{}`), &value)

	err := sch.Validate(value)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := DescribeValidationError(sch, err)
	if strings.Contains(msg, "valid choices") {
		t.Errorf("hint invented for a property with no enum: %q", msg)
	}
}

// combineInputsSchemas wraps multiple cascade layers in allOf; the hint must
// still be found by searching through allOf branches.
func TestDescribeValidationError_AllOfWrappedSchema(t *testing.T) {
	sch := mustCompile(t, `{"allOf": [
		{"type":"object","additionalProperties":false,"required":["task"],
		 "properties":{"task":{"type":"string","enum":["work","none"]}}},
		{"type":"object"}
	]}`)
	var value map[string]any
	_ = json.Unmarshal([]byte(`{}`), &value)

	err := sch.Validate(value)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := DescribeValidationError(sch, err)
	if !strings.Contains(msg, `"task": valid choices are work, none`) {
		t.Errorf("message missing enum hint for allOf-wrapped schema: %q", msg)
	}
}
