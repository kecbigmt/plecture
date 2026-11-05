package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/publicoutput"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestUp_PublicOutputsFollowTheNodesThatProduceThem(t *testing.T) {
	store := testStore(t)
	endpointFile := filepath.Join(t.TempDir(), "endpoint")
	if err := os.WriteFile(endpointFile, []byte("e1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := writeWorkflowFixture(t, "default",
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
	if outputs, _ := publicoutput.Of(got, publicoutput.ProducedOnly); outputs["endpoint"] != "e1" {
		t.Fatalf("public outputs after up = %v, want endpoint e1", outputs)
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
	if outputs, _ := publicoutput.Of(store.Get(name), publicoutput.ProducedOnly); len(outputs) != 0 {
		t.Fatalf("public outputs after down = %v, want the released source's binding unset", outputs)
	}

	if err := os.WriteFile(endpointFile, []byte("e2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up after down: %v", err)
	}
	got = store.Get(name)
	if outputs, _ := publicoutput.Of(got, publicoutput.ProducedOnly); outputs["endpoint"] != "e2" {
		t.Fatalf("public outputs after recreation = %v, want endpoint e2", outputs)
	}
	if got.Nodes["relay"].Inputs["peer"] != "e2" {
		t.Fatalf("relay input peer after recreation = %v, want e2", got.Nodes["relay"].Inputs["peer"])
	}
}

func TestUp_ConsumerOfAPublicOutputWaitsForItsProducerEvenWhenDeclaredFirst(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "default",
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
	cfg := writeWorkflowFixture(t, "default",
		[]taskFixture{{id: "agent", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "agent", inputs: map[string]*lang.Value{"peer": {Form: lang.FormFrom, From: "workflow.outputs.endpoint"}}}})
	addWorkflowFields(t, cfg, "default", "\n[default.outputs]\nendpoint = { from = \"nodes.agent.outputs.endpoint\" }\n")
	_, err := cfg.LoadWorkflows("")
	if err == nil || !strings.Contains(err.Error(), "PLECTURE-CFG-WORKFLOW-CYCLE") {
		t.Fatalf("LoadWorkflows = %v, want a cycle diagnostic", err)
	}
}

func TestDestroy_ReleasesADependentOfAFailedSourceThroughItsPublicOutput(t *testing.T) {
	store := testStore(t)
	marker := filepath.Join(t.TempDir(), "released")
	cfg := writeWorkflowFixture(t, "default",
		[]taskFixture{
			{id: "y", scope: "run", setup: `echo '{"k":"v1"}'`, cleanup: "true"},
			{id: "x", scope: "run", setup: `echo '{}'`, cleanup: `printf %s {{.Workflow.outputs.k}} > ` + marker},
		},
		[]nodeFixture{
			{id: "y"},
			{id: "x", inputs: map[string]*lang.Value{"dep": {Form: lang.FormFrom, From: "workflow.outputs.k"}}},
		})
	addWorkflowFields(t, cfg, "default", "\n[default.outputs]\nk = { from = \"nodes.y.outputs.k\" }\n")
	name := "work-683"
	seedSessionWithNodes(t, store, name, "acct", 683, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := store.Update(name, func(s *domain.Session) error {
		s.Nodes["y"].Status = contract.TaskStatusFailed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: name}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "v1" {
		t.Fatalf("x's cleanup marker = %q, %v; want x released with the failed source's last output", got, err)
	}
}
