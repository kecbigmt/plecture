package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

func TestLoadResourceDefsDecodesSharedQueryContract(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "source.toml"), []byte(`[source]
kind = "resource_observer"
match = "^urn:case:"
[source.observe]
type = "exec"
command = "true"
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
[source.query.subscribe]
type = "exec"
command = "printf"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	defs, err := (&Config{BaseDir: base}).LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs: %v", err)
	}
	query := defs["source"].Query
	if query == nil || query.Poll == nil || query.Subscribe == nil {
		t.Fatalf("query = %+v, want both means", query)
	}
	if query.ItemSchema["type"] != "object" || query.InputsSchema["type"] != "object" {
		t.Fatalf("query schemas = %+v / %+v", query.InputsSchema, query.ItemSchema)
	}
}

func TestLoadWorkflowsDecodesPopulationDefaultsAndDurations(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "agent.toml"), []byte(`[source]
kind = "resource_observer"
match = "^urn:case:"
[source.observe]
type = "exec"
command = "true"
[source.query.inputs_schema]
type = "object"
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

[base_work]
kind = "task"
resource_observer = "source"

[work]
kind = "task"
extends = "base_work"

[agent]
kind = "workflow"
workspace_provider = "provider"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
uses = ["poll"]
poll_every = "45s"
[agent.populations.query]
scope = "all"
[agent.populations.session]
task = "work"
[agent.populations.session.destroy]
force = true
[agent.populations.session.inputs]
resource = { from = "resource.id" }
context = { from = "item.context", optional = true }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	workflows, err := (&Config{BaseDir: base}).LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows: %v", err)
	}
	population := workflows["agent"].Populations[0]
	if population.Name != "dispatch" || population.ResourceObserver != "source" || population.Session.Task != "work" {
		t.Fatalf("population = %+v", population)
	}
	if population.PollEvery.Duration.String() != "45s" || population.ExpireAfter.Duration != 0 {
		t.Fatalf("durations = %s / %s", population.PollEvery.Duration, population.ExpireAfter.Duration)
	}
	if population.AutoDown || population.AutoDestroy || !population.Session.Destroy.Force {
		t.Fatalf("permissions/defaults = %+v", population)
	}
	if population.Session.Inputs["resource"].From != "resource.id" {
		t.Fatalf("inputs = %+v", population.Session.Inputs)
	}
	if len(population.Uses) != 1 || population.Uses[0] != "poll" {
		t.Fatalf("uses = %+v", population.Uses)
	}
}

func TestPopulationDurationsMustBePositive(t *testing.T) {
	for _, field := range []string{"poll_every", "expire_after"} {
		t.Run(field, func(t *testing.T) {
			base := t.TempDir()
			src := "[agent]\nkind = \"workflow\"\n[[agent.populations]]\nname = \"dispatch\"\nresource_observer = \"source\"\n" + field + " = \"0s\"\n[agent.populations.query]\n"
			if err := os.WriteFile(filepath.Join(base, "agent.toml"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := (&Config{BaseDir: base}).LoadWorkflows(""); err == nil {
				t.Fatalf("expected %s = 0s to fail", field)
			}
		})
	}
}

func TestLoadWorkflowsEnforcesPopulationContracts(t *testing.T) {
	tests := []struct {
		name       string
		population string
	}{
		{
			name: "query parameters",
			population: `uses = ["poll"]
poll_every = "1m"
[agent.populations.query]
wrong = "all"
`,
		},
		{
			name: "query timing",
			population: `uses = ["poll"]
expire_after = "1h"
[agent.populations.query]
scope = "all"
`,
		},
		{
			name: "initial task observer",
			population: `uses = ["poll"]
poll_every = "1m"
[agent.populations.query]
scope = "all"
[agent.populations.session]
task = "work"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			definitions := `[source]
kind = "resource_observer"
match = "^urn:case:"
[source.observe]
type = "exec"
command = "true"
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
[source.query.poll]
type = "exec"
command = "true"

[other]
kind = "resource_observer"
match = "^urn:case:"
[other.observe]
type = "exec"
command = "true"

[work]
kind = "task"
resource_observer = "other"

[agent]
kind = "workflow"
workspace_provider = "provider"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
` + tt.population
			if err := os.WriteFile(filepath.Join(base, "definitions.toml"), []byte(definitions), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := (&Config{BaseDir: base}).LoadWorkflows("")
			var diagnostic *lang.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != lang.CodePopulationContract {
				t.Fatalf("LoadWorkflows error = %v, want %s", err, lang.CodePopulationContract)
			}
		})
	}
}
