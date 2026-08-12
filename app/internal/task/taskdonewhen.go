package task

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// RenderOutputsTemplate renders a template over the session's persisted
// outputs view (`.Workflow.outputs.*` / `.Nodes.<id>.outputs.*`) with
// missingkey=zero semantics. The result is whitespace-trimmed; "<no value>"
// (nil renders) is stripped.
func RenderOutputsTemplate(expr string, workflowOutputs map[string]any, nodes map[string]*contract.TaskState) (string, error) {
	tmpl, err := template.New("outputs_template").
		Option("missingkey=zero").
		Funcs(templateFuncs).
		Parse(expr)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	nodeView := make(map[string]map[string]any, len(nodes))
	for id, st := range nodes {
		if st == nil {
			continue
		}
		outputs := normalizeOutputs(st.Outputs)
		if outputs == nil {
			outputs = map[string]any{}
		}
		nodeView[id] = map[string]any{"outputs": outputs}
	}
	wfOutputs := normalizeOutputs(workflowOutputs)
	if wfOutputs == nil {
		wfOutputs = map[string]any{}
	}
	data := struct {
		Workflow map[string]any
		Nodes    map[string]map[string]any
	}{
		Workflow: map[string]any{"outputs": wfOutputs},
		Nodes:    nodeView,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return strings.TrimSpace(strings.ReplaceAll(buf.String(), "<no value>", "")), nil
}
