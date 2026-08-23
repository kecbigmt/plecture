package task

import (
	"context"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// taskStub is a terse spec for one effect declaration used by buildPlan.
// Each script field becomes a shell action, the form these declarations
// actually take; a test that cares about the exec form builds the action
// itself through setupAction. Scope defaults to "run" so most callers can
// omit it.
type taskStub struct {
	id      string
	scope   string
	setup   string
	cleanup string
	alive   string
	// setupAction overrides setup with an action the test states itself.
	setupAction *lang.Action
	// attach/capture/sendText/sendKeys build a `[terminal]` table when any is
	// set. A verb is available on its own, so a stub may declare only the
	// ones its case consumes.
	attach            string
	capture           string
	sendText          string
	sendKeys          string
	outputsSchema     map[string]any
	outputsSchemaFile string
	inputsSchema      map[string]any
	inputsSchemaFile  string
	baseDir           string
}

// nodeStub mirrors config.WorkflowNode but uses positional convenience so
// tests read like "build a workflow with these nodes" rather than threading
// `uses` defaults by hand. Empty `uses` defaults to `id`.
type nodeStub struct {
	id     string
	uses   string
	inputs map[string]*lang.Value
}

// buildPlan compiles a workflow with the given nodes against in-memory task
// definitions and asserts the compile succeeded. Replaces the old
// `Validate([]TaskConfig)` ergonomic for tests that exercise the planner
// directly. `nodes` is the per-workflow node list; each taskStub yields one
// definition (id is shared with the matching node when `uses` is omitted).
func buildPlan(t *testing.T, defs []taskStub, nodes []nodeStub) *Plan {
	t.Helper()
	plan, err := tryBuildPlan(defs, nodes)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	return plan
}

func tryBuildPlan(defs []taskStub, nodes []nodeStub) (*Plan, error) {
	defMap := make(map[string]config.TaskDefinition, len(defs))
	for _, s := range defs {
		var terminal *config.TerminalConfig
		if s.attach != "" || s.capture != "" || s.sendText != "" || s.sendKeys != "" {
			terminal = &config.TerminalConfig{
				Attach:   shellStub(s.attach),
				Capture:  shellStub(s.capture),
				SendText: shellStub(s.sendText),
				SendKeys: shellStub(s.sendKeys),
			}
		}
		setup := s.setupAction
		if setup == nil {
			setup = shellStub(s.setup)
		}
		defMap[s.id] = config.TaskDefinition{
			ID:                s.id,
			Scope:             s.scope,
			Setup:             setup,
			Cleanup:           shellStub(s.cleanup),
			Health:            healthStub(s.alive),
			Terminal:          terminal,
			OutputsSchema:     s.outputsSchema,
			OutputsSchemaFile: s.outputsSchemaFile,
			InputsSchema:      s.inputsSchema,
			InputsSchemaFile:  s.inputsSchemaFile,
			BaseDir:           s.baseDir,
		}
	}
	wfNodes := make([]config.WorkflowNode, 0, len(nodes))
	for _, n := range nodes {
		// The loader defaults each of the two from the other; a stub states
		// whichever one its case is about.
		id, uses := n.id, n.uses
		if id == "" {
			id = uses
		}
		if uses == "" {
			uses = id
		}
		wfNodes = append(wfNodes, config.WorkflowNode{ID: id, Uses: uses, Inputs: n.inputs})
	}
	wf := config.WorkflowFile{ID: "test", Nodes: wfNodes}
	return CompileWorkflow(wf, defMap)
}

func mustMount(id, dir string, executables ...plugins.Executable) plugins.Mounted {
	return plugins.Mounted{ID: id, Dir: dir, Manifest: plugins.Manifest{Executables: executables}}
}

// shellStub is one shell action carrying a literal script, or nil for a
// stub that declares none.
func shellStub(script string) *lang.Action {
	if script == "" {
		return nil
	}
	return &lang.Action{Type: lang.ActionShell, Script: script}
}

// healthStub builds the `[health]` table a stub's alive probe implies, or nil
// when the stub declares none.
func healthStub(alive string) *config.HealthConfig {
	if alive == "" {
		return nil
	}
	return &config.HealthConfig{Alive: shellStub(alive)}
}

// shellExecution is the invocation a `bash -c` script resolves to, stated
// here because the tests that assert on the host path's own shape are the
// only remaining callers that need to build one by hand.
func shellExecution(script string) *lang.Execution {
	return &lang.Execution{Argv: []string{"bash", "-c", script}}
}

// scriptedExecutor answers each request from a queue keyed by the rendered
// command, so a multi-layer run can hand every layer its own stdout while the
// recorded requests still show the order the layers ran in.
type scriptedExecutor struct {
	requests []effect.ExecRequest
	scripts  []string
	bindings []string
	// stdout maps a resolved script to the stdout it produces. A script with
	// no entry produces "{}".
	stdout map[string]string
	// failOn makes the named script exit non-zero.
	failOn string
}

func (s *scriptedExecutor) Run(_ context.Context, req effect.ExecRequest) ([]byte, []byte, error) {
	s.requests = append(s.requests, req)
	cmd, bindings := effect.ReadShellRun(req)
	s.scripts = append(s.scripts, cmd)
	s.bindings = append(s.bindings, bindings)
	if s.failOn != "" && cmd == s.failOn {
		return nil, []byte("boom"), errNestedTestFailure
	}
	if out, ok := s.stdout[cmd]; ok {
		return []byte(out), nil, nil
	}
	return []byte("{}"), nil, nil
}

type nestedTestFailure struct{}

func (nestedTestFailure) Error() string { return "exit status 1" }

var errNestedTestFailure = nestedTestFailure{}

func withScriptedExecutor(t *testing.T, e *scriptedExecutor) *scriptedExecutor {
	t.Helper()
	restore := effect.UseExecutor(e)
	t.Cleanup(restore)
	return e
}

// commands reports what each recorded execution ran, read while its run
// directory still existed.
func (s *scriptedExecutor) commands() []string {
	return s.scripts
}

// reset forgets everything recorded so far, for a case that drives setup and
// then asserts only on what the cleanup ran.
func (s *scriptedExecutor) reset() {
	s.requests = nil
	s.scripts = nil
	s.bindings = nil
}

// nestedPlan compiles a one-node plan whose task is the outermost of the
// given chain. defs are ordered outermost-first; each is linked to the next
// by `inner`, and the chain is stamped the way config load stamps it.
func nestedPlan(t *testing.T, defs ...config.TaskDefinition) []Resolved {
	t.Helper()
	for i := range defs[:len(defs)-1] {
		defs[i].Inner = defs[i+1].ID
	}
	outer := defs[0]
	outer.InnerChain = defs[1:]
	r, err := ResolveDefinition(outer, outer.ID)
	if err != nil {
		t.Fatalf("ResolveDefinition: %v", err)
	}
	return []Resolved{r}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		schema["required"] = req
	}
	return schema
}
