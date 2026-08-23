package lang

import (
	"fmt"
	"os"
	"path/filepath"
)

// DiscoverWorkspaceOverlay reads the cloned, untrusted workspace-dir .plect/
// overlay. It is not a full definition root: only kind = "workflow"
// fragments under <root>/workflows/ are loaded, under the workflow cascade
// rules (see layer.go); the directory is only an allowlist, so nothing
// outside it is even read. A kind = "effect" fragment there is a load error,
// because cloned content must not carry shell. A workspace_provider,
// resource_observer, or channel fragment is not loaded at all — not an
// error, simply absent from the result. A missing root is not an error: it
// means the clone carries no overlay.
func DiscoverWorkspaceOverlay(root string) ([]*Definition, error) {
	workflowsDir := filepath.Join(root, "workflows")
	if _, err := os.Stat(workflowsDir); os.IsNotExist(err) {
		return nil, nil
	}
	all, err := DiscoverRoot(workflowsDir, nil)
	if err != nil {
		return nil, err
	}
	fragments := make([]*Definition, 0, len(all))
	for _, def := range all {
		switch def.Kind {
		case KindWorkflow:
			fragments = append(fragments, def)
		case KindEffect:
			return nil, fmt.Errorf("%s: a workspace overlay may not declare an effect definition (%q); cloned content must not carry shell", def.File, def.ID)
		case KindWorkspaceProvider, KindResourceObserver, KindChannel, KindTask:
			// Not loaded at all: declarations.md's trust boundary for
			// this overlay.
		}
	}
	return fragments, nil
}
