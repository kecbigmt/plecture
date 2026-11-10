package service

import (
	"os"
	"path/filepath"
	"testing"
)

const prTicketURL = "https://forge.example/tickets/7"

func prProjectResource(t *testing.T, f *wdFixture) string {
	t.Helper()
	root := pcProject(t, f)
	wdWrite(t, filepath.Join(root, ".plect", "resources", "ticket.toml"), `
[ticket]
kind  = "resource"
match = '^https://forge\.example/tickets/(?P<number>\d+)$'
name  = { expr = "'ticket-' + match.number" }

[ticket.state_schema]
type       = "object"
properties = { status = { type = "string" } }

[ticket.observe]
type    = "exec"
command = "sh"
args    = ["-c", 'printf "{\"status\":\"open\"}"']

[ticket.finalize]
type    = "exec"
command = "sh"
args    = ["-c", 'touch "$HOME/finalized"']
`)
	wdWrite(t, filepath.Join(root, ".plect", "workflows", "ticketed.toml"), `
[ticketed]
kind     = "workflow"
resource = "ticket"

[[ticketed.nodes]]
uses = "planted"
`)
	wdWrite(t, filepath.Join(root, ".plect", "tasks", "ticket_work.toml"), `
[ticket_work]
kind         = "task"
description  = "Work a ticket"
resource     = "ticket"
instructions = [{ text = "Work {{ resource.id }}." }]

[ticket_work.done_when]
all = [{ check = "resource.state.status", in = ["open"] }]
`)
	return root
}

func TestProjectResource_ASessionCreatedFromTheRootResolvesItFromTheRecordedRoot(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	root := prProjectResource(t, f)

	if _, err := Up(f.cfg, f.store, UpParams{Identifier: prTicketURL, ProjectRoot: root, InitialTask: &InitialTask{TaskID: "ticket_work"}}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	session := pcSession(t, f.store, "ticket-7")
	if session.ProjectRoot != root {
		t.Errorf("ProjectRoot = %q, want %q", session.ProjectRoot, root)
	}
	rows, err := f.store.ListSubscriptions("ticket-7", "ticket")
	if err != nil || len(rows) != 2 || rows[0].Source != "dispatch" || rows[1].Source != "task" {
		t.Fatalf("entry subscriptions = %+v, err = %v", rows, err)
	}

	observations, err := ObserveSessionResources(f.cfg, f.store, "ticket-7")
	if err != nil {
		t.Fatalf("ObserveSessionResources: %v", err)
	}
	if len(observations) != 1 || observations[0].Error != "" {
		t.Fatalf("observations = %+v, want one successful observation of the project resource", observations)
	}

	result, err := FinalizeTask(f.cfg, f.store, FinalizeTaskParams{SessionName: "ticket-7", Instance: "initial"})
	if err != nil {
		t.Fatalf("FinalizeTask: %v", err)
	}
	if !result.Finalized || result.Definition != "ticket" {
		t.Errorf("finalize = %+v, want the project resource's finalize hook to run", result)
	}
	if _, err := os.Stat(filepath.Join(f.home, "finalized")); err != nil {
		t.Errorf("the project resource's finalize hook did not run: %v", err)
	}
}

func TestProjectResource_ResourceStatusResolvesItOnlyInItsRoot(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	root := prProjectResource(t, f)

	result, err := ResourceStatus(f.cfg, ResourceStatusParams{ResourceID: prTicketURL, ProjectRoot: root})
	if err != nil {
		t.Fatalf("ResourceStatus in the root: %v", err)
	}
	if result.Definition != "ticket" || result.State["status"] != "open" {
		t.Errorf("ResourceStatus = %+v, want the project resource's observation", result)
	}

	if _, err := ResourceStatus(f.cfg, ResourceStatusParams{ResourceID: prTicketURL}); err == nil {
		t.Error("ResourceStatus in the global-only context recognized a resource only the project root declares")
	}
}

func TestProjectResource_IsNotVisibleOutsideItsRoot(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	prProjectResource(t, f)

	if _, err := Up(f.cfg, f.store, UpParams{Identifier: prTicketURL, Workflow: "ticketed"}); err == nil {
		t.Fatal("Up in the global-only context created a session for a resource only the project root declares")
	}
	if s, _ := f.store.GetE("ticket-7"); s != nil {
		t.Errorf("session ticket-7 created in context %q", s.ProjectRoot)
	}
}
