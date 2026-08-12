package commands

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plect/app/internal/task"
)

// taskDisplayName shows the instance key. A named instance keys on the name
// itself (name == instance), so only the key is shown; the parameter is kept so
// a future display-only label can surface alongside the numbered key.
func taskDisplayName(name, instance string) string {
	if name != "" && name != instance {
		return fmt.Sprintf("%s (%s)", name, instance)
	}
	return instance
}

// formatDoneWhen renders a per-instance done_when evaluation compactly, e.g.
// "✓ satisfied (2/2)" / "✗ unsatisfied (1/2) [worktree_dirty=2]". A check whose
// value isn't observed yet shows "?" — it reads pending, not failing.
func formatDoneWhen(dw *task.DoneWhenResult) string {
	satisfied := 0
	var vals []string
	for _, leaf := range dw.Leaves {
		if leaf.Status == task.DoneSatisfied {
			satisfied++
		}
		if leaf.Kind == "check" {
			v := leaf.Value
			if !leaf.Observed {
				v = "?"
			}
			vals = append(vals, leaf.Output+"="+v)
		}
		if leaf.Kind == "judge" {
			vals = append(vals, formatDoneWhenJudge(leaf))
		}
	}
	sym := "⋯"
	switch dw.Overall {
	case task.DoneSatisfied:
		sym = "✓"
	case task.DoneUnsatisfied:
		sym = "✗"
	}
	out := fmt.Sprintf("%s %s (%d/%d)", sym, dw.Overall, satisfied, len(dw.Leaves))
	if len(vals) > 0 {
		out += " [" + strings.Join(vals, " ") + "]"
	}
	return out
}

func formatDoneWhenJudge(leaf task.DoneLeafResult) string {
	k := leaf.ID
	if k == "" {
		k = "judge"
	}
	v := leaf.Action
	if v == "" {
		v = leaf.PendingReason
	}
	if v == "" {
		v = string(leaf.Status)
	}
	if leaf.Reason != "" {
		v += ":" + leaf.Reason
	}
	switch {
	case leaf.Revision != "" && leaf.CurrentRevision != "" && leaf.Revision != leaf.CurrentRevision:
		v += "@" + leaf.Revision + "!=" + leaf.CurrentRevision
	case leaf.Revision != "":
		v += "@" + leaf.Revision
	case leaf.CurrentRevision != "":
		v += "@current:" + leaf.CurrentRevision
	}
	if leaf.PendingReason != "" && leaf.Action != "" {
		v += "/" + leaf.PendingReason
	}
	return k + "=" + v
}
