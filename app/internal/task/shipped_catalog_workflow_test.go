package task

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

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

// TestShippedCatalog_WorkflowNodeInputsMatchTaskSchema skips a node whose
// `uses` names a task id the official catalog doesn't ship — an
// operator-supplied composition point (e.g. goal_review.toml's envfile/
// slack_thread/initial_task nodes) has no official schema to check against.
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

// renderable reports whether every node an input's `.Nodes.<id>.outputs`
// reference names is already in resolved: RenderInputs uses
// missingkey=error, so an unresolved reference fails the render rather than
// being skipped.
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

// synthesizeOutputs fabricates a type-correct dummy per declared output so a
// downstream node's input template can render without a real task run.
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

// inputsSchemaKeys returns a nil allowed set when additionalProperties
// doesn't restrict extra keys, matching the schema's own semantics.
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
