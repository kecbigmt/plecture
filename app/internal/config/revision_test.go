package config

import (
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestWorkflowRevisionCanonicalBodyAndRoundTrip(t *testing.T) {
	first := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Nodes: []WorkflowNode{{ID: "one", Uses: "effect"}}, Populations: []WorkflowPopulation{{Name: "dynamic"}}, Healthcheck: &HealthcheckConfig{Period: Duration{Duration: time.Second}}},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect", Scope: TaskScopeRun}},
		Tasks:    map[string]TaskDocument{"task": {ID: "task", Instruction: "Do the work."}},
	}
	second := WorkflowRevision{
		Workflow: first.Workflow,
		Effects:  map[string]TaskDefinition{"effect": {Scope: TaskScopeRun, ID: "effect"}},
		Tasks:    map[string]TaskDocument{"task": {Instruction: "Do the work.", ID: "task"}},
	}
	oneBody, oneDigest, err := first.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	twoBody, twoDigest, err := second.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if string(oneBody) != string(twoBody) || oneDigest != twoDigest {
		t.Fatalf("equal declarations differ: %q/%s, %q/%s", oneBody, oneDigest, twoBody, twoDigest)
	}
	if strings.Contains(string(oneBody), "dynamic") {
		t.Fatalf("population declaration entered revision: %s", oneBody)
	}
	decoded, err := DecodeWorkflowRevision(oneBody, oneDigest)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Tasks["task"].Instruction != "Do the work." || decoded.Effects["effect"].Scope != TaskScopeRun {
		t.Fatalf("revision lost declarations: %+v", decoded)
	}
	if decoded.Workflow.Healthcheck.Period.Duration != time.Second {
		t.Fatalf("healthcheck period = %v", decoded.Workflow.Healthcheck.Period.Duration)
	}
	if _, err := DecodeWorkflowRevision(oneBody, strings.Repeat("0", 64)); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestWorkflowRevisionDigestIgnoresNonExecutableSourceLocations(t *testing.T) {
	first := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", SourcePath: "/workspace/one/work.toml", BaseDir: "/workspace/one", Display: map[string]*lang.Value{
			"title": {Form: lang.FormLiteral, Literal: "work", Pos: lang.Position{File: "/workspace/one/work.toml", Path: "work.display.title"}},
		}},
		Effects: map[string]TaskDefinition{"effect": {ID: "effect", SourcePath: "/workspace/one/effect.toml", BaseDir: "/workspace/one"}},
		Tasks:   map[string]TaskDocument{"task": {ID: "task", SourcePath: "/workspace/one/task.toml", BaseDir: "/workspace/one"}},
	}
	second := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", SourcePath: "/workspace/two/work.toml", BaseDir: "/workspace/two", Display: map[string]*lang.Value{
			"title": {Form: lang.FormLiteral, Literal: "work", Pos: lang.Position{File: "/workspace/two/work.toml", Path: "work.display.title"}},
		}},
		Effects: map[string]TaskDefinition{"effect": {ID: "effect", SourcePath: "/workspace/two/effect.toml", BaseDir: "/workspace/two"}},
		Tasks:   map[string]TaskDocument{"task": {ID: "task", SourcePath: "/workspace/two/task.toml", BaseDir: "/workspace/two"}},
	}
	_, oneDigest, err := first.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	_, twoDigest, err := second.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if oneDigest != twoDigest {
		t.Fatalf("same declarations at different paths got digests %q and %q", oneDigest, twoDigest)
	}
}

func TestWorkflowRevisionCanonicalBodyKeepsUTF8AndHTMLCharacters(t *testing.T) {
	revision := WorkflowRevision{Workflow: WorkflowFile{ID: "work", Address: "work", Description: "<>& café"}}
	body, _, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "<>& café") {
		t.Fatalf("canonical body escaped declaration text: %s", body)
	}
}

func TestSessionWorkflowAndRunScopeUseStoredRevision(t *testing.T) {
	revision := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", Nodes: []WorkflowNode{{ID: "original", Uses: "effect"}}},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect", Scope: TaskScopeRun}},
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	session := &domain.Session{Workflow: "work", WorkflowRevision: digest, WorkflowRevisionBody: string(body),
		Nodes: map[string]*contract.TaskState{"original": {Scope: TaskScopeRun, Status: contract.TaskStatusProduced}}}
	cfg := &Config{}
	wf, err := cfg.ResolveSessionWorkflow(session)
	if err != nil || wf == nil || len(wf.Nodes) != 1 || wf.Nodes[0].ID != "original" {
		t.Fatalf("stored workflow = %+v, err %v", wf, err)
	}
	if !cfg.RunScopeUp(session) {
		t.Fatal("stored run-scoped node was ignored")
	}
}
