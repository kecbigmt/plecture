package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// TaskDetail is the static picture `plect task show <id>` presents: the
// task's identity and, when it is nested or an extension, the chain of
// definitions that compose it.
type TaskDetail struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Scope and Nesting are an effect's: a task document owns no lifecycle,
	// so it has neither.
	Scope string `json:"scope,omitempty"`
	// ResourceObserver is the observer a task document is written for,
	// resolved through extends when this document is an extension.
	ResourceObserver string      `json:"resource_observer,omitempty"`
	SourcePath       string      `json:"source_path,omitempty"`
	Nesting          []TaskLayer `json:"nesting,omitempty"`
	// ExtendsChain is the composed extends chain, outermost (this document)
	// first, each entry naming only what that declaration itself contributes
	// — the per-element provenance a composed instruction, chain, or judge
	// leaf traces back to.
	ExtendsChain []TaskExtendsLayer `json:"extends_chain,omitempty"`
}

// TaskLayer is one layer of a nesting chain, with the `inner` reference as
// the author wrote it — the resolution is what the reader is checking, so the
// reference and the file it resolved to are shown side by side.
type TaskLayer struct {
	ID         string `json:"id"`
	Inner      string `json:"inner,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
}

// TaskExtendsLayer is one layer of an extends chain: the declaration's own
// contribution, before composition folds every layer's instructions, chains,
// and judge leaves together into the document a session actually runs.
type TaskExtendsLayer struct {
	ID          string   `json:"id"`
	SourcePath  string   `json:"source_path,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
	Chains      []string `json:"chains,omitempty"`
	Judges      []string `json:"judges,omitempty"`
}

func taskExtendsChain(doc config.TaskDocument) []TaskExtendsLayer {
	layers := doc.ExtendsLayers
	chain := make([]TaskExtendsLayer, 0, len(layers))
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		entry := TaskExtendsLayer{ID: layer.ID, SourcePath: layer.SourcePath, Instruction: layer.Instruction}
		for _, ch := range layer.Chains {
			entry.Chains = append(entry.Chains, ch.ID)
		}
		if layer.DoneWhen != nil {
			for _, leaf := range layer.DoneWhen.All {
				if leaf.IsJudge() {
					entry.Judges = append(entry.Judges, leaf.ID)
				}
			}
		}
		chain = append(chain, entry)
	}
	return chain
}

// TaskShow resolves one task definition from the cascade rooted at
// workspaceDirPath.
func TaskShow(cfg *config.Config, workspaceDirPath, id string) (*TaskDetail, error) {
	docs, defs, err := cfg.LoadTaskDeclarations(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load task declarations: %w", err)
	}
	if doc, ok := docs[id]; ok {
		// Inspecting a declaration is when a reader wants to know whether it
		// holds together, so the contract checks that need the rest of the
		// layer run here rather than waiting for an instantiation. Extends
		// composition already ran inside LoadTaskDeclarations, so doc's
		// Instruction, Chains, and DoneWhen are already the composed ones.
		observers, oerr := cfg.LoadResourceDefs()
		if oerr != nil {
			return nil, fmt.Errorf("load resource observers: %w", oerr)
		}
		workflows, werr := cfg.LoadWorkflows(workspaceDirPath)
		if werr != nil {
			return nil, fmt.Errorf("load workflows: %w", werr)
		}
		if verr := cfg.ValidateTaskDocuments(docs, observers, workflows); verr != nil {
			return nil, verr
		}
		return &TaskDetail{
			ID:               doc.ID,
			Kind:             string(lang.KindTask),
			ResourceObserver: doc.ResourceObserver,
			SourcePath:       doc.SourcePath,
			ExtendsChain:     taskExtendsChain(doc),
		}, nil
	}
	def, ok := defs[id]
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q not found", id)}
	}
	detail := &TaskDetail{ID: def.ID, Kind: string(lang.KindEffect), Scope: def.EffectiveScope(), SourcePath: def.SourcePath}
	if def.IsNested() {
		detail.Nesting = append(detail.Nesting, TaskLayer{ID: def.ID, Inner: def.Inner, SourcePath: def.SourcePath})
		for _, layer := range def.InnerChain {
			detail.Nesting = append(detail.Nesting, TaskLayer{ID: layer.ID, Inner: layer.Inner, SourcePath: layer.SourcePath})
		}
	}
	return detail, nil
}
