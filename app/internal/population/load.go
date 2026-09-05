// Package population evaluates workflow-owned desired session populations.
package population

import (
	"fmt"
	"sort"
	"strings"

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
	InputsSchema  *jsonschema.Schema
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
	observer, ok := observers[p.ResourceObserver]
	if !ok {
		return def, fmt.Errorf("resource_observer names unknown definition %q", p.ResourceObserver)
	}
	if observer.Query == nil {
		return def, fmt.Errorf("resource observer %q declares no query face", p.ResourceObserver)
	}
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
	switch {
	case query.Poll != nil:
		if p.PollEvery.Duration <= 0 {
			return def, fmt.Errorf("poll_every is required for a poll-backed query")
		}
		if p.ExpireAfter.Duration > 0 {
			return def, fmt.Errorf("expire_after is forbidden for a poll-backed query")
		}
	case query.Subscribe != nil:
		if p.ExpireAfter.Duration <= 0 {
			return def, fmt.Errorf("expire_after is required for a subscribe-only query")
		}
		if p.PollEvery.Duration > 0 {
			return def, fmt.Errorf("poll_every is forbidden for a subscribe-only query")
		}
	}

	var err error
	def.InputsSchema, err = lang.CompileInlineSchema(query.InputsSchema, "plect:population:"+wf.Address+":"+p.Name+":query-inputs")
	if err != nil {
		return def, fmt.Errorf("query inputs_schema: %w", err)
	}
	if err := def.InputsSchema.Validate(p.Query); err != nil {
		return def, fmt.Errorf("query parameters: %w", err)
	}
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
	itemProperties, err := config.SchemaProperties(query.ItemSchema, "")
	if err != nil {
		return def, err
	}
	stateProperties, err := config.SchemaProperties(observer.StateSchema, observer.ResolvedStateSchemaPath())
	if err != nil {
		return def, err
	}
	for property := range itemProperties {
		if _, overlaps := stateProperties[property]; property != "resource" && overlaps {
			return def, fmt.Errorf("query item property %q duplicates a state_schema fact; observe is the state authority", property)
		}
	}
	for key, value := range p.Session.Inputs {
		if value.Form == lang.FormLiteral {
			continue
		}
		if value.Form != lang.FormFrom {
			return def, fmt.Errorf("session input %q must be a literal or a resource.id/item.* projection", key)
		}
		if value.From == "resource.id" {
			continue
		}
		property, ok := strings.CutPrefix(value.From, "item.")
		if !ok || strings.Contains(property, ".") {
			return def, fmt.Errorf("session input %q reads unsupported projection %q", key, value.From)
		}
		if _, declared := itemProperties[property]; !declared {
			return def, fmt.Errorf("session input %q reads %q, which item_schema does not declare", key, value.From)
		}
	}
	if p.Session.Task != "" {
		doc, ok := docs[p.Session.Task]
		if !ok {
			return def, fmt.Errorf("session.task names unknown task %q", p.Session.Task)
		}
		if doc.ResourceObserver != p.ResourceObserver {
			return def, fmt.Errorf("session.task observer %q differs from population observer %q", doc.ResourceObserver, p.ResourceObserver)
		}
		def.InitialTask = &doc
	}
	return def, nil
}
