package task

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// applyWorkdir marks the preparation nodes and gives every other node the
// workdir. The preparation set is read off the graph before any default edge
// exists: a default edge into the producer from one of its own prerequisites
// would close a cycle, and a node outside the set cannot be a prerequisite of
// anything inside it.
func applyWorkdir(wf config.WorkflowFile, nodes []Resolved) error {
	ref := wf.Workdir
	if ref == nil {
		return nil
	}
	byID := make(map[string]int, len(nodes))
	for i := range nodes {
		byID[nodes[i].NodeID] = i
	}
	if _, ok := byID[ref.Node]; !ok {
		return fmt.Errorf("workflow %q: workdir reads unknown node %q", wf.ID, ref.Node)
	}
	prep := map[string]bool{ref.Node: true}
	queue := []string{ref.Node}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range nodes[byID[cur]].DependsOn {
			if !prep[dep] {
				prep[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	for i := range nodes {
		n := &nodes[i]
		if prep[n.NodeID] {
			n.Prep = true
			continue
		}
		n.Workdir = ref
		if !hasDep(n.DependsOn, ref.Node) {
			n.DependsOn = append(n.DependsOn, ref.Node)
		}
	}
	return nil
}

// WorkdirOf evaluates a workflow's `workdir` against the produced outputs of
// its producer node. A workflow that declares none has no directory, which is
// reported as "" rather than an error.
func WorkdirOf(ref *lang.WorkdirRef, tasks map[string]*contract.TaskState) (string, error) {
	if ref == nil {
		return "", nil
	}
	producer := tasks[ref.Node]
	if producer == nil || producer.Status != contract.TaskStatusProduced {
		return "", fmt.Errorf("workdir: node %q is not produced", ref.Node)
	}
	value, ok := producer.Outputs[ref.Key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("workdir: node %q has no string output %q", ref.Node, ref.Key)
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("workdir: node %q output %q is %q, which is not an absolute path", ref.Node, ref.Key, value)
	}
	return value, nil
}

// setupDir is the directory a node's setup runs in. A preparation node runs
// in the current user's home directory rather than anywhere a caller or a
// daemon happened to be, so its setup depends on no directory it might be
// creating. A workflow with no workdir gives its nodes no directory at all.
func (r Resolved) setupDir(tasks map[string]*contract.TaskState) (string, error) {
	switch {
	case r.Prep:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("preparation node %q: %w", r.NodeID, err)
		}
		return home, nil
	case r.Workdir != nil:
		return WorkdirOf(r.Workdir, tasks)
	}
	return "", nil
}
