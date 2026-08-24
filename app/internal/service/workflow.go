package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// WorkflowSummary is a row in WorkflowList output: just enough to pick the
// right workflow without parsing the whole TOML.
type WorkflowSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	AutoSelect  bool   `json:"auto_select"`
}

// WorkflowDetail is the full picture for `plect workflow show <id>` and
// `plect_workflow_show`: identity, the merged inputs_schema (so callers can build
// `--inputs`), and the DAG (so agents can reason about node ordering).
type WorkflowDetail struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// WorkspaceProvider is the referenced workspace provider's id; the
	// resolved workspace provider (resolver + hooks) rides along so
	// `workflow show` can present the whole picture without a second lookup.
	WorkspaceProvider     string                          `json:"workspace_provider,omitempty"`
	WorkspaceProviderInfo *config.WorkspaceProviderConfig `json:"workspace_provider_info,omitempty"`
	// WorkspaceProviderError is set instead of WorkspaceProviderInfo, never
	// both, so a load failure cannot make `workflow show` abort rendering
	// the rest of an otherwise-loadable workflow.
	WorkspaceProviderError string            `json:"workspace_provider_error,omitempty"`
	Display                map[string]string `json:"display,omitempty"`
	AutoSelect             bool              `json:"auto_select"`
	InputsSchema           map[string]any    `json:"inputs_schema,omitempty"`
	Nodes                  []WorkflowNode    `json:"nodes"`
	Channels               []WorkflowChannel `json:"channels,omitempty"`
	// Tick is the workflow's declared [tick] table (docs/wiki/verification-gate.md),
	// nil when undeclared.
	Tick *config.TickConfig `json:"tick,omitempty"`
}

// WorkflowChannel is the show-time view of an [[event.channel]]: its name, the
// channel definition it uses (with the resolved primitive type), the event
// types it delivers, and the input bindings.
type WorkflowChannel struct {
	Name    string            `json:"name"`
	Uses    string            `json:"uses"`
	Type    string            `json:"type,omitempty"`
	Include []string          `json:"include,omitempty"`
	Inputs  map[string]string `json:"inputs,omitempty"`
}

// WorkflowNode is the show-time view of a workflow node: which task it uses,
// scope, the input bindings, and the derived depends_on list.
type WorkflowNode struct {
	ID        string            `json:"id"`
	Uses      string            `json:"uses"`
	Scope     string            `json:"scope"`
	DependsOn []string          `json:"depends_on,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
}

// WorkflowList returns all discoverable workflows merged from the cascade
// rooted at workspaceDirPath, sorted by id for stable output. An empty
// workspaceDirPath still surfaces the global layer.
func WorkflowList(cfg *config.Config, workspaceDirPath string) ([]WorkflowSummary, error) {
	workflows, err := cfg.LoadWorkflows(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load workflows: %w", err)
	}
	out := make([]WorkflowSummary, 0, len(workflows))
	for _, wf := range workflows {
		out = append(out, WorkflowSummary{
			ID:          wf.Address,
			Name:        wf.Name,
			Description: wf.Description,
			AutoSelect:  workflowAutoSelect(wf),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// WorkflowShow resolves a single workflow and compiles it so the returned
// DAG matches what `plect up` would actually execute. Returns an ErrInvalidInput
// service error when the id isn't present in the cascade (mirrors the
// "workflow not found" path in tasks.go's buildWorkflowPlan), or when a
// node's input wiring has drifted from its task's inputs_schema — the same
// mismatch RunSetup would otherwise only catch once dispatch reaches that
// node, by which point earlier nodes may already have run.
func WorkflowShow(cfg *config.Config, workspaceDirPath, id string) (*WorkflowDetail, error) {
	workflows, err := cfg.LoadWorkflows(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load workflows: %w", err)
	}
	wf, ok := workflows[id]
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found", id)}
	}
	defs, err := cfg.LoadTaskDefinitions(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load task definitions: %w", err)
	}
	plan, err := task.CompileWorkflow(wf, defs)
	if err != nil {
		return nil, fmt.Errorf("compile workflow %q: %w", id, err)
	}
	if svcErr := validateNodeInputsStatic(id, plan); svcErr != nil {
		return nil, svcErr
	}
	// A node shows the reference its author wrote, not the resolved
	// definition's id: two plugins may declare that id, so the id alone would
	// not say which declaration this node runs.
	references := make(map[string]string, len(wf.Nodes))
	for _, node := range wf.Nodes {
		references[node.ID] = node.Uses
	}
	nodes := make([]WorkflowNode, 0, len(plan.Session)+len(plan.Run))
	for _, r := range plan.Session {
		nodes = append(nodes, resolvedToNode(r, references[r.NodeID]))
	}
	for _, r := range plan.Run {
		nodes = append(nodes, resolvedToNode(r, references[r.NodeID]))
	}
	detail := &WorkflowDetail{
		ID:                wf.Address,
		Name:              wf.Name,
		Description:       wf.Description,
		WorkspaceProvider: wf.WorkspaceProvider,
		Display:           valueSources(wf.Display),
		AutoSelect:        workflowAutoSelect(wf),
		InputsSchema:      wf.InputsSchema,
		Nodes:             nodes,
		Tick:              wf.Tick,
	}
	if wf.WorkspaceProvider != "" {
		workspaceProviders, err := cfg.LoadWorkspaceProviders()
		if err != nil {
			detail.WorkspaceProviderError = err.Error()
		} else if prov, ok := workspaceProviders[wf.WorkspaceProvider]; ok {
			detail.WorkspaceProviderInfo = &prov
		}
	}
	if len(wf.Event.Channel) > 0 {
		channels, err := cfg.LoadChannels()
		if err != nil {
			return nil, fmt.Errorf("load channels: %w", err)
		}
		if err := config.ValidateWorkflowChannels(wf, channels); err != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: err.Error()}
		}
		for _, ch := range wf.Event.Channel {
			view := WorkflowChannel{
				Name:    ch.Name,
				Uses:    ch.Uses,
				Include: ch.Include,
				Inputs:  valueSources(ch.Inputs),
			}
			if def, ok := channels[ch.Uses]; ok {
				view.Type = def.Type
			}
			detail.Channels = append(detail.Channels, view)
		}
	}
	return detail, nil
}

func validateNodeInputsStatic(id string, plan *task.Plan) *Error {
	var lines []string
	for _, group := range [][]task.Resolved{plan.Session, plan.Run} {
		for _, r := range group {
			for _, issue := range task.ValidateInputsStatic(r) {
				lines = append(lines, fmt.Sprintf("workflow %q: node %q: %s", id, r.NodeID, issue.Message))
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return &Error{Code: ErrInvalidInput, Message: strings.Join(lines, "\n")}
}

func resolvedToNode(r task.Resolved, reference string) WorkflowNode {
	if reference == "" {
		reference = r.TaskID
	}
	return WorkflowNode{
		ID:        r.NodeID,
		Uses:      reference,
		Scope:     r.Scope,
		DependsOn: append([]string(nil), r.DependsOn...),
		Inputs:    valueSources(r.Inputs),
	}
}

// valueSources renders a value table the way its author wrote it, for a
// listing that shows a configuration to a person rather than evaluating it.
func valueSources(values map[string]*lang.Value) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value.Source()
	}
	return out
}
