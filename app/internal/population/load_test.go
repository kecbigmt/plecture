package population

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func writeDefinition(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func populationConfig(t *testing.T, queryMeans, timing, sessionInputs string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeDefinition(t, dir, "observer", `[source]
kind = "resource"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)(#[a-z]+)?$"
name = { from = "match.id" }
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
additionalProperties = false
[source.query.item_schema.properties.resource]
type = "string"
[source.query.item_schema.properties.context]
type = "string"
`+queryMeans)
	writeDefinition(t, dir, "runtime", `[runtime]
kind = "effect"
scope = "run"
[runtime.setup]
type = "exec"
command = "true"
[runtime.health.alive]
type = "noop"
`)
	writeDefinition(t, dir, "workflow", `[agent]
kind = "workflow"
resource = "source"
[agent.inputs_schema]
type = "object"
required = ["resource"]
additionalProperties = false
[agent.inputs_schema.properties.resource]
type = "string"
[[agent.nodes]]
id = "runtime"
uses = "runtime"
[[agent.populations]]
name = "dispatch"
`+timing+`
[agent.populations.query]
scope = "all"
[agent.populations.session.inputs]
`+sessionInputs)
	return &config.Config{BaseDir: dir}
}

func TestLoadAcceptsPollPopulationAndCompilesContracts(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "printf"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)

	defs, err := Load(cfg, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(defs) != 1 || defs[0].Workflow.Address != "agent" || defs[0].Entry.ID != "source" {
		t.Fatalf("definitions = %+v", defs)
	}
}

func TestLoadRequiresTimingForTheUsesSelection(t *testing.T) {
	tests := []struct {
		name   string
		means  string
		timing string
		want   string
	}{
		{"poll requires poll_every", "[source.query.poll]\ntype = \"exec\"\ncommand = \"true\"\n", `uses = ["poll"]`, "poll_every"},
		{"poll forbids expire_after", "[source.query.poll]\ntype = \"exec\"\ncommand = \"true\"\n", "uses = [\"poll\"]\npoll_every = \"1m\"\nexpire_after = \"1h\"", "expire_after"},
		{"subscribe-only requires expire_after", "[source.query.subscribe]\ntype = \"exec\"\ncommand = \"true\"\n", `uses = ["subscribe"]`, "expire_after"},
		{"subscribe-only forbids poll_every", "[source.query.subscribe]\ntype = \"exec\"\ncommand = \"true\"\n", "uses = [\"subscribe\"]\npoll_every = \"1m\"\nexpire_after = \"1h\"", "poll_every"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(populationConfig(t, tt.means, tt.timing, `resource = { from = "resource.id" }`), "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsAUsesSelectionTheObserverDoesNotDeclare(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, `uses = ["subscribe"]`, `resource = { from = "resource.id" }`)
	_, err := Load(cfg, "")
	if err == nil || !strings.Contains(err.Error(), "subscribe") {
		t.Fatalf("error = %v, want a diagnostic naming the undeclared subscribe means", err)
	}
}

func TestLoadRejectsUnknownItemProjection(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "item.missing" }`)

	_, err := Load(cfg, "")
	if err == nil || !strings.Contains(err.Error(), "item.missing") {
		t.Fatalf("error = %v, want unknown item projection", err)
	}
}

func TestLoadReadsThePopulationsOfTheSelectedProjectRoot(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "printf"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	root := filepath.Join(t.TempDir(), "widgets")
	if err := os.MkdirAll(filepath.Join(root, ".plect"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDefinition(t, filepath.Join(root, ".plect"), "project", "schema_version = 2\n")
	if err := os.Rename(filepath.Join(cfg.BaseDir, "workflow.toml"), filepath.Join(root, ".plect", "workflow.toml")); err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProjectRoots = []string{root}

	defs, err := Load(cfg, root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(defs) != 1 || defs[0].ProjectRoot != root || populationKey(defs[0]).ProjectRoot != root {
		t.Fatalf("defs = %+v, want the project's population keyed by its root", defs)
	}
	global, err := Load(cfg, "")
	if err != nil || len(global) != 0 {
		t.Fatalf("Load(global-only) = %d defs, %v; want none", len(global), err)
	}
}
