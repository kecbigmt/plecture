package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// TaskDetail is the static picture `plect task show <id>` presents: the
// task's identity and, when it is nested, the chain of definitions that
// compose it.
type TaskDetail struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Scope and Nesting are an effect's: a task document owns no lifecycle,
	// so it has neither.
	Scope string `json:"scope,omitempty"`
	// ResourceObserver is the observer a task document is written for.
	ResourceObserver string      `json:"resource_observer,omitempty"`
	SourcePath       string      `json:"source_path,omitempty"`
	Nesting          []TaskLayer `json:"nesting,omitempty"`
}

// TaskLayer is one layer of a nesting chain, with the `inner` reference as
// the author wrote it — the resolution is what the reader is checking, so the
// reference and the file it resolved to are shown side by side.
type TaskLayer struct {
	ID         string `json:"id"`
	Inner      string `json:"inner,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
}

// TaskShow resolves one task definition from the cascade rooted at
// workspaceDirPath.
func TaskShow(cfg *config.Config, workspaceDirPath, id string) (*TaskDetail, error) {
	docs, err := cfg.LoadTaskDocuments(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load task documents: %w", err)
	}
	if doc, ok := docs[id]; ok {
		return &TaskDetail{
			ID:               doc.ID,
			Kind:             string(lang.KindTask),
			ResourceObserver: doc.ResourceObserver,
			SourcePath:       doc.SourcePath,
		}, nil
	}
	defs, err := cfg.LoadTaskDefinitions(workspaceDirPath)
	if err != nil {
		return nil, fmt.Errorf("load task definitions: %w", err)
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
