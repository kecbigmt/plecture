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
	// ProjectRoot is the configuration context that declares the
	// population, empty for the global-only context.
	ProjectRoot string
	Workflow    config.WorkflowFile
	Population  config.WorkflowPopulation
	// Entry is the workflow's entry resource. It is both the query the
	// population runs and the type every admitted item must resolve to, so the
	// two can never name different resources.
	Entry         config.ResourceDef
	InitialTask   *config.TaskDocument
	ItemSchema    *jsonschema.Schema
	SessionSchema *jsonschema.Schema
}

// Load resolves every active population of one configuration context and
// validates every cross-definition contract before a resident evaluator
// starts.
func Load(cfg *config.Config, projectRoot string) ([]Definition, error) {
	workflows, err := cfg.LoadWorkflows(projectRoot)
	if err != nil {
		return nil, err
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, err
	}
	docs, err := cfg.LoadTaskDocuments(projectRoot)
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
			def, err := resolveDefinition(projectRoot, wf, p, observers, docs)
			if err != nil {
				return nil, fmt.Errorf("workflow %q population %q: %w", address, p.Name, err)
			}
			out = append(out, def)
		}
	}
	return out, nil
}

func resolveDefinition(projectRoot string, wf config.WorkflowFile, p config.WorkflowPopulation, observers map[string]config.ResourceDef, docs map[string]config.TaskDocument) (Definition, error) {
	def := Definition{ProjectRoot: projectRoot, Workflow: wf, Population: p}
	entry, ok := observers[wf.Resource]
	if !ok {
		return def, fmt.Errorf("workflow resource names unknown definition %q", wf.Resource)
	}
	def.Entry = entry

	query := entry.Query
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
