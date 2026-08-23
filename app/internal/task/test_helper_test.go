package task

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
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
