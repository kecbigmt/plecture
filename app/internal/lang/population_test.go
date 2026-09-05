package lang

import (
	"errors"
	"testing"
)

func validateUserSource(t *testing.T, src string) error {
	t.Helper()
	defs, err := ParseDefinitionDocument("test.toml", []byte(src))
	if err != nil {
		return err
	}
	v := Validation{Executables: NewExecutableRegistry()}
	for _, def := range defs {
		if err := v.ValidateDefinition(def); err != nil {
			return err
		}
	}
	return nil
}

func TestResourceObserverQueryRequiresSharedSchemasAndAMeans(t *testing.T) {
	base := `[source]
kind = "resource_observer"
match = "^urn:"
[source.observe]
type = "exec"
command = "true"
`
	tests := []struct {
		name string
		body string
	}{
		{"inputs schema", `[source.query]
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
[source.query.poll]
type = "exec"
command = "true"
`},
		{"item schema", `[source.query]
[source.query.inputs_schema]
type = "object"
[source.query.poll]
type = "exec"
command = "true"
`},
		{"means", `[source.query]
[source.query.inputs_schema]
type = "object"
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUserSource(t, base+tt.body)
			var d *Diagnostic
			if !errors.As(err, &d) || d.Code != CodeFieldRequired {
				t.Fatalf("error = %v, want %s", err, CodeFieldRequired)
			}
		})
	}
}

func TestResourceObserverQueryAcceptsPollAndSubscribeWithSharedInputs(t *testing.T) {
	err := validateUserSource(t, `[source]
kind = "resource_observer"
match = "^urn:"
[source.observe]
type = "exec"
command = "true"
[source.query.inputs_schema]
type = "object"
required = ["scope"]
[source.query.inputs_schema.properties.scope]
type = "string"
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
[source.query.poll]
type = "exec"
command = "printf"
args = [{ from = "inputs.scope" }]
[source.query.subscribe]
type = "exec"
command = "printf"
args = [{ from = "inputs.scope" }]
`)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestResourceObserverQueryRejectsUndeclaredInputProjection(t *testing.T) {
	for _, projection := range []string{`{ from = "inputs.missing" }`, `{ expr = "inputs.missing" }`} {
		t.Run(projection, func(t *testing.T) {
			err := validateUserSource(t, `[source]
kind = "resource_observer"
[source.query.inputs_schema]
type = "object"
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
[source.query.poll]
type = "exec"
command = "printf"
		args = [`+projection+`]
`)
			var d *Diagnostic
			if !errors.As(err, &d) || d.Code != CodeFromPath {
				t.Fatalf("error = %v, want %s", err, CodeFromPath)
			}
		})
	}
}

func TestResourceObserverQueryRejectsStateFactsInItems(t *testing.T) {
	err := validateUserSource(t, `[source]
kind = "resource_observer"
match = "^urn:"
[source.observe]
type = "exec"
command = "true"
[source.state_schema]
type = "object"
[source.state_schema.properties.status]
type = "string"
[source.query.inputs_schema]
type = "object"
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
[source.query.item_schema.properties.status]
type = "string"
[source.query.poll]
type = "exec"
command = "true"
`)
	if err == nil {
		t.Fatal("expected overlapping query item and state property to fail")
	}
}

func TestWorkflowPopulationSurfaceAndTrustedOwnership(t *testing.T) {
	src := `[agent]
kind = "workflow"
workspace_provider = "provider"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
poll_every = "1m"
[agent.populations.query]
scope = "all"
[agent.populations.session]
task = "work"
[agent.populations.session.inputs]
resource = { from = "resource.id" }
context = { from = "item.context", optional = true }
`
	if err := validateUserSource(t, src); err != nil {
		t.Fatalf("user-owned population: %v", err)
	}

	defs, err := ParseDefinitionDocument("test.toml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	err = (Validation{From: Ownership{IsPlugin: true}}).ValidateDefinition(defs[0])
	if err == nil {
		t.Fatal("plugin-owned population must be rejected")
	}
}

func TestWorkflowPopulationRequiresUniqueNameObserverAndQuery(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"name", `resource_observer = "source"
[agent.populations.query]
scope = "all"`},
		{"observer", `name = "dispatch"
[agent.populations.query]
scope = "all"`},
		{"query", `name = "dispatch"
resource_observer = "source"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUserSource(t, `[agent]
kind = "workflow"
[[agent.populations]]
`+tt.body)
			var d *Diagnostic
			if !errors.As(err, &d) || d.Code != CodeFieldRequired {
				t.Fatalf("error = %v, want %s", err, CodeFieldRequired)
			}
		})
	}

	err := validateUserSource(t, `[agent]
kind = "workflow"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
[agent.populations.query]
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
[agent.populations.query]
`)
	var d *Diagnostic
	if !errors.As(err, &d) || d.Code != CodeIDDuplicate {
		t.Fatalf("duplicate error = %v, want %s", err, CodeIDDuplicate)
	}
}

func TestWorkflowPopulationPlanValidatesQueryAndItemContracts(t *testing.T) {
	src := `[source]
kind = "resource_observer"
[source.query.inputs_schema]
type = "object"
required = ["scope"]
additionalProperties = false
[source.query.inputs_schema.properties.scope]
type = "string"
[source.query.item_schema]
type = "object"
required = ["resource"]
[source.query.item_schema.properties.resource]
type = "string"
[source.query.item_schema.properties.context]
type = "string"
[source.query.poll]
type = "exec"
command = "true"

[provider]
kind = "workspace_provider"

[agent]
kind = "workflow"
workspace_provider = "provider"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
poll_every = "1m"
[agent.populations.query]
scope = "all"
[agent.populations.session.inputs]
context = { from = "item.context" }
`
	defs, err := ParseDefinitionDocument("test.toml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(nil, defs)
	var workflow *Definition
	for _, def := range defs {
		if err := (Validation{}).ValidateDefinition(def); err != nil {
			t.Fatal(err)
		}
		if def.ID == "agent" {
			workflow = def
		}
	}
	if err := (Validation{}).ValidatePlan(workflow, registry); err != nil {
		t.Fatalf("valid population contract: %v", err)
	}

	workflow.Body["populations"].([]map[string]any)[0]["query"] = map[string]any{"wrong": "value"}
	err = (Validation{}).ValidatePlan(workflow, registry)
	var d *Diagnostic
	if !errors.As(err, &d) || d.Code != CodePopulationContract {
		t.Fatalf("query error = %v, want %s", err, CodePopulationContract)
	}
}

func TestCompileInlineSchemaAcceptsDeclaredEmptySchema(t *testing.T) {
	schema, err := CompileInlineSchema(map[string]any{}, "plect:test-empty-schema")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(map[string]any{"any": "literal query value"}); err != nil {
		t.Fatalf("empty schema rejected a value: %v", err)
	}
}
