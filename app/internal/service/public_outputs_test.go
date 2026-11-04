package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

func TestUp_PublicOutputsFollowTheNodesThatProduceThem(t *testing.T) {
	store := testStore(t)
	endpointFile := filepath.Join(t.TempDir(), "endpoint")
	if err := os.WriteFile(endpointFile, []byte("e1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "agent", scope: "run", setup: `printf '{"endpoint":"%s"}' "$(cat ` + endpointFile + `)"`, cleanup: "true"},
			{id: "relay", scope: "run", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{
			{id: "agent"},
			{id: "relay", inputs: map[string]*lang.Value{"peer": {Form: lang.FormFrom, From: "workflow.outputs.endpoint"}}},
		})
	addWorkflowFields(t, cfg, "default", "\n[default.outputs]\nendpoint = { from = \"nodes.agent.outputs.endpoint\" }\n")
	name := "work-681"
	seedSessionWithNodes(t, store, name, "acct", 681, "default", nil)

	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	got := store.Get(name)
	if got.PublicOutputs["endpoint"] != "e1" {
		t.Fatalf("PublicOutputs after up = %v, want endpoint e1", got.PublicOutputs)
	}
	if got.Nodes["relay"].Inputs["peer"] != "e1" {
		t.Fatalf("relay input peer = %v, want the public output's value", got.Nodes["relay"].Inputs["peer"])
	}
	if deps := got.Nodes["relay"].DependsOn; len(deps) != 1 || deps[0] != "agent" {
		t.Fatalf("relay DependsOn = %v, want the public output's producer", deps)
	}

	if _, err := Down(cfg, store, DownParams{Identifier: name}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if got := store.Get(name); len(got.PublicOutputs) != 0 {
		t.Fatalf("PublicOutputs after down = %v, want the released source's binding invalidated", got.PublicOutputs)
	}

	if err := os.WriteFile(endpointFile, []byte("e2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up after down: %v", err)
	}
	got = store.Get(name)
	if got.PublicOutputs["endpoint"] != "e2" {
		t.Fatalf("PublicOutputs after recreation = %v, want endpoint e2", got.PublicOutputs)
	}
	if got.Nodes["relay"].Inputs["peer"] != "e2" {
		t.Fatalf("relay input peer after recreation = %v, want e2", got.Nodes["relay"].Inputs["peer"])
	}
}

func TestUp_ConsumerOfAPublicOutputWaitsForItsProducerEvenWhenDeclaredFirst(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "agent", scope: "run", setup: `echo '{"endpoint":"e1"}'`, cleanup: "true"},
			{id: "relay", scope: "run", setup: `echo '{}'`, cleanup: "true"},
		},
		[]nodeFixture{
			{id: "relay", inputs: map[string]*lang.Value{"peer": {Form: lang.FormFrom, From: "workflow.outputs.endpoint"}}},
			{id: "agent"},
		})
	addWorkflowFields(t, cfg, "default", "\n[default.outputs]\nendpoint = { from = \"nodes.agent.outputs.endpoint\" }\n")
	name := "work-682"
	seedSessionWithNodes(t, store, name, "acct", 682, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := store.Get(name).Nodes["relay"].Inputs["peer"]; got != "e1" {
		t.Fatalf("relay input peer = %v, want e1", got)
	}
}

func TestLoadWorkflows_CycleThroughAPublicOutputFailsTheWorkflowLoad(t *testing.T) {
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "agent", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "agent", inputs: map[string]*lang.Value{"peer": {Form: lang.FormFrom, From: "workflow.outputs.endpoint"}}}})
	addWorkflowFields(t, cfg, "default", "\n[default.outputs]\nendpoint = { from = \"nodes.agent.outputs.endpoint\" }\n")
	_, err := cfg.LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-WORKFLOW-CYCLE") {
		t.Fatalf("LoadWorkflows = %v, want a cycle diagnostic", err)
	}
}
