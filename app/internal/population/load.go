// Package population evaluates workflow-owned desired session populations.
package population

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/task"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Definition is one fully resolved workflow population ready to evaluate.
type Definition struct {
	Workflow      config.WorkflowFile
	Population    config.WorkflowPopulation
	Observer      config.ResourceDef
	Provider      config.WorkspaceProviderConfig
	InitialTask   *config.TaskDocument
	ItemSchema    *jsonschema.Schema
	SessionSchema *jsonschema.Schema
}

// Load resolves every active population from the workspace-independent user
// workflow layer and validates every cross-definition contract before a
// resident evaluator starts.
func Load(cfg *config.Config) ([]Definition, error) {
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return nil, err
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, err
	}
	providers, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		return nil, err
	}
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateTaskDocuments(docs, observers, workflows); err != nil {
		return nil, err
	}

	addresses := config.Addresses(workflows)
	sort.Strings(addresses)
	var out []Definition
	for _, address := range addresses {
		wf := workflows[address]
		for _, p := range wf.Populations {
			def, err := resolveDefinition(wf, p, observers, providers, docs)
			if err != nil {
				return nil, fmt.Errorf("workflow %q population %q: %w", address, p.Name, err)
			}
			out = append(out, def)
		}
	}
	return out, nil
}

func resolveDefinition(wf config.WorkflowFile, p config.WorkflowPopulation, observers map[string]config.ResourceDef, providers map[string]config.WorkspaceProviderConfig, docs map[string]config.TaskDocument) (Definition, error) {
	def := Definition{Workflow: wf, Population: p}
	observer := observers[p.ResourceObserver]
	def.Observer = observer
	provider, ok := providers[wf.WorkspaceProvider]
	if !ok {
		return def, fmt.Errorf("workflow workspace_provider names unknown definition %q", wf.WorkspaceProvider)
	}
	if !provider.HasResolver() {
		return def, fmt.Errorf("workspace provider %q has no resource resolver", wf.WorkspaceProvider)
	}
	def.Provider = provider

	query := observer.Query
	var err error
	def.ItemSchema, err = lang.CompileInlineSchema(query.ItemSchema, "plect:population:"+wf.Address+":"+p.Name+":items")
	if err != nil {
		return def, fmt.Errorf("query item_schema: %w", err)
	}
	def.SessionSchema, err = lang.CompileSchema(wf.InputsSchema, wf.ResolvedInputsSchemaPath(), "plect:population:"+wf.Address+":"+p.Name+":session-inputs")
	if err != nil {
		return def, fmt.Errorf("workflow inputs_schema: %w", err)
	}
	if issues := task.ValidateInputsStatic(task.Resolved{Inputs: p.Session.Inputs, InputsSchema: def.SessionSchema}); len(issues) > 0 {
		return def, fmt.Errorf("session inputs: %s", issues[0].Message)
	}
	if p.Session.Task != "" {
		doc := docs[p.Session.Task]
		def.InitialTask = &doc
	}
	return def, nil
}
