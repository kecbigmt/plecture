package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := os.WriteFile(filepath.Join(base, "agent.toml"), []byte(`[agent]
kind = "workflow"
workspace_provider = "provider"
[[agent.populations]]
name = "dispatch"
resource_observer = "source"
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
