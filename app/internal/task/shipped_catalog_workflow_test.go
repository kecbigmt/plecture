package task

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// loadShippedCatalogWorkflows loads this repository's own official catalog
// workflows alongside its task definitions, reusing loadShippedCatalogTasks's
// plugin-mount setup so both walk the exact same catalog.toml plugin list.
func loadShippedCatalogWorkflows(t *testing.T) (map[string]config.WorkflowFile, map[string]config.TaskDefinition) {
	t.Helper()
	tasks, mounted := loadShippedCatalogTasks(t)
	pluginDirs := make([]string, len(mounted))
	for i, m := range mounted {
		pluginDirs[i] = m.Dir
	}
	cfg := &config.Config{PluginDirs: pluginDirs, Plugins: mounted}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatalf("LoadWorkflows(shipped catalog): %v", err)
	}
	return workflows, tasks
}

// TestShippedCatalog_WorkflowNodeInputsMatchTaskSchema guards every shipped
// workflow's [[nodes]] input bindings against the inputs_schema of whichever
// official task they resolve to. A node whose `uses` names a task id the
// official catalog does not ship (an operator-supplied composition point —
// see e.g. goal_review.toml's own header comment) is skipped: there is no
// official schema to check it against.
//
// Two checks run per resolvable node: a structural key/required check
// against the raw input template map (works regardless of whether upstream
// nodes are resolvable, so it always catches an unsupported/missing key),
// and — only when every `.Nodes.<id>.outputs` reference the node's inputs
// make is itself resolvable — an actual template render against synthetic
// upstream outputs, validated through the same compiled schema RunSetup
// uses. The second check additionally catches template syntax errors and
// pattern/type violations the first one can't see.
//
// This is a regression test for the class of bug the okf plugin's
// goal_review workflow shipped with: passing slack_thread_ts/slack_channel_id
// to the codex_exec node when agent/codex's codex_exec task never declared
// those inputs, so additionalProperties=false rejected them the first time a
// session actually reached that node — a failure RunSetup only surfaces
// after upstream nodes have already run, never at `workflow show`/compile
// time.
func TestShippedCatalog_WorkflowNodeInputsMatchTaskSchema(t *testing.T) {
	workflows, tasks := loadShippedCatalogWorkflows(t)
	session := SessionVars{Name: "test-session"}

	checked := 0
	for wfID, wf := range workflows {
		resolvedOutputs := map[string]map[string]any{}
		for _, node := range wf.Nodes {
			uses := node.Uses
			if uses == "" {
				uses = node.ID
			}
			def, ok := tasks[uses]
			if !ok {
				continue
			}
			checked++
			nodeID := node.ID
			if nodeID == "" {
				nodeID = uses
			}
			allowed, required := inputsSchemaKeys(def.InputsSchema)
			for key := range node.Inputs {
				if allowed != nil && !allowed[key] {
					t.Errorf("workflow %q node %q: input %q is not accepted by task %q's inputs_schema", wfID, nodeID, key, uses)
				}
			}
			for _, key := range required {
				if _, set := node.Inputs[key]; !set {
					t.Errorf("workflow %q node %q: task %q requires input %q, but the node does not set it", wfID, nodeID, uses, key)
				}
			}

			if renderable(node.Inputs, resolvedOutputs) {
				rendered, err := RenderInputs(node.Inputs, resolvedOutputs, nil, session)
				if err != nil {
					t.Errorf("workflow %q node %q: render inputs: %v", wfID, nodeID, err)
				} else if schema, err := CompileSchema(def.InputsSchema, def.ResolvedInputsSchemaPath(), "test:"+uses); err != nil {
					t.Errorf("workflow %q node %q: compile task %q inputs_schema: %v", wfID, nodeID, uses, err)
				} else if schema != nil {
					if err := schema.Validate(toJSONShape(rendered)); err != nil {
						t.Errorf("workflow %q node %q: rendered inputs rejected by task %q's inputs_schema: %v", wfID, nodeID, uses, err)
					}
				}
			}
			resolvedOutputs[nodeID] = synthesizeOutputs(def.OutputsSchema)
		}
	}
	if checked == 0 {
		t.Fatal("no shipped workflow node resolved against an official task; this test is checking nothing")
	}
}

// renderable reports whether every `.Nodes.<id>.outputs` reference in inputs
// names a node already present in resolved — i.e. whether RenderInputs can
// run without hitting missingkey=error on an operator-supplied node this
// test never synthesized outputs for.
func renderable(inputs map[string]string, resolved map[string]map[string]any) bool {
	for _, tmpl := range inputs {
		for _, m := range nodeRefRE.FindAllStringSubmatch(tmpl, -1) {
			if _, ok := resolved[m[1]]; !ok {
				return false
			}
		}
	}
	return true
}

// synthesizeOutputs fabricates a schema-conformant value for each declared
// outputs_schema property, keyed by declared JSON type, so a downstream
// node's input template can render against something type-correct without
// this test needing to know what a real task setup would actually produce.
func synthesizeOutputs(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(props))
	for name, raw := range props {
		spec, _ := raw.(map[string]any)
		switch spec["type"] {
		case "integer", "number":
			out[name] = 1
		case "boolean":
			out[name] = true
		default:
			out[name] = "synthetic-" + name
		}
	}
	return out
}

// inputsSchemaKeys extracts an inline inputs_schema's declared property
// names and required list. allowed is nil when the schema does not restrict
// extra properties (additionalProperties unset or true) — nil means "any key
// passes", matching the schema's own semantics.
func inputsSchemaKeys(schema map[string]any) (allowed map[string]bool, required []string) {
	if schema == nil {
		return nil, nil
	}
	if raw, ok := schema["required"].([]any); ok {
		for _, r := range raw {
			if s, ok := r.(string); ok {
				required = append(required, s)
			}
		}
	}
	addl, hasAddl := schema["additionalProperties"].(bool)
	if !hasAddl || addl {
		return nil, required
	}
	props, _ := schema["properties"].(map[string]any)
	allowed = make(map[string]bool, len(props))
	for k := range props {
		allowed[k] = true
	}
	return allowed, required
}
