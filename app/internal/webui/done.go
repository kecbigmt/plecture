package webui

import (
	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/app/internal/task"
)

// doneStatusClass maps a done_when status to badge color classes, mirroring the
// CLI's ✓/✗/⋯ semantics. Tailwind's built-in palette (like statusClass) because
// the neutral token set carries no success/danger pair.
func doneStatusClass(s task.DoneStatus) string {
	switch s {
	case task.DoneSatisfied:
		return "bg-green-100 text-green-800"
	case task.DoneUnsatisfied:
		return "bg-red-100 text-red-800"
	default:
		return "bg-gray-100 text-gray-700"
	}
}

// doneSymbol reuses the CLI's compact glyph so web and terminal read alike:
// ✓ satisfied, ✗ unsatisfied, ⋯ pending (predicate not yet evaluable).
func doneSymbol(s task.DoneStatus) string {
	switch s {
	case task.DoneSatisfied:
		return "✓"
	case task.DoneUnsatisfied:
		return "✗"
	default:
		return "⋯"
	}
}

// taskName renders a task instance's display name. A named instance keys on its
// own name, so only the key shows; a numbered instance with a distinct name
// shows "name (instance)". Mirrors the CLI's taskDisplayName.
func taskName(name, instance string) string {
	if name != "" && name != instance {
		return name + " (" + instance + ")"
	}
	return instance
}

// doneSummary rolls up done_when leaves or tasks into a satisfied count and an
// aggregate status (unsatisfied if any part is, else pending if any part is,
// else satisfied) — the same precedence the engine uses across a task's leaves.
type doneSummary struct {
	Status    task.DoneStatus
	Satisfied int
	Total     int
}

// leafStats summarizes one task's already-evaluated leaves for its detail badge.
// It counts satisfied leaves only; the overall status comes straight from the
// projection, so the web never re-evaluates the predicate.
func leafStats(dw *task.DoneWhenResult) doneSummary {
	s := doneSummary{Status: dw.Overall, Total: len(dw.Leaves)}
	for _, l := range dw.Leaves {
		if l.Status == task.DoneSatisfied {
			s.Satisfied++
		}
	}
	return s
}

// gateSummary rolls up a session's done_when-bearing tasks for its list card.
// nil when the session has none, so the card shows no gate badge.
func gateSummary(tasks []service.TaskInstanceView) *doneSummary {
	s := &doneSummary{}
	anyUnsatisfied, anyPending := false, false
	for _, t := range tasks {
		if t.DoneWhen == nil {
			continue
		}
		s.Total++
		switch t.DoneWhen.Overall {
		case task.DoneSatisfied:
			s.Satisfied++
		case task.DoneUnsatisfied:
			anyUnsatisfied = true
		default:
			anyPending = true
		}
	}
	if s.Total == 0 {
		return nil
	}
	switch {
	case anyUnsatisfied:
		s.Status = task.DoneUnsatisfied
	case anyPending:
		s.Status = task.DonePending
	default:
		s.Status = task.DoneSatisfied
	}
	return s
}
