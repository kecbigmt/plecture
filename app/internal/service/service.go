package service

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/publicoutput"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// LatestStatusMessage derives a session's status line from its most recent
// plect.status_message event, or nil if none (or a read error).
func LatestStatusMessage(store *state.Store, sessionName string) *domain.Message {
	ev, ok, err := eventlog.NewStore(store.Dir()).LatestByType(sessionName, event.TypeStatusMessage)
	if err != nil || !ok {
		return nil
	}
	if ev.Metadata["cleared"] == "true" || ev.Summary == "" {
		return nil
	}
	return &domain.Message{Text: ev.Summary, UpdatedAt: ev.Time}
}

var validTag = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateTagFormat returns ErrInvalidTag for non-empty tags that don't match
// the allowed character set. Empty tags are caller-validated (the "no tag"
// case is legal at the API surface).
func validateTagFormat(tag string) *Error {
	if !validTag.MatchString(tag) {
		return &Error{Code: ErrInvalidTag, Message: fmt.Sprintf("invalid tag %q: must match [a-zA-Z0-9_-]+", tag)}
	}
	return nil
}

// effectiveTag resolves the session-identity tag that becomes part of a
// session name. An explicit --tag wins; otherwise the workflow id is the
// default, so two workflows acting on one resource (a work workflow, a
// review workflow) materialize distinct sessions instead of racing for one
// branch/workspace.
// The tag is never empty on the resource-dispatch paths — session
// identity always carries a label.
func effectiveTag(tag, workflowID string) (string, *Error) {
	if tag != "" {
		if err := validateTagFormat(tag); err != nil {
			return "", err
		}
		return tag, nil
	}
	if err := validateTagFormat(workflowID); err != nil {
		return "", &Error{Code: ErrInvalidTag, Message: fmt.Sprintf("workflow id %q cannot seed a session tag (must match [a-zA-Z0-9_-]+); pass --tag explicitly", workflowID)}
	}
	return workflowID, nil
}

// resolveSession resolves an identifier (session name, create-time alias, or
// resource id) to a session from the store. Lookup order:
//
//  1. exact session name
//  2. create-time alias (survives resolver rule changes; ambiguous when tag
//     variants share the alias)
//  3. resource derivation (pure, offline — works during external service
//     outages)
func resolveSession(cfg *config.Config, store *state.Store, identifier string) (string, *domain.Session, error) {
	if session, err := store.GetE(identifier); err != nil {
		return "", nil, err
	} else if session != nil {
		return identifier, session, nil
	}

	if hits, err := store.FindByAliasE(identifier); err != nil {
		return "", nil, err
	} else if len(hits) == 1 {
		return hits[0].Name, hits[0], nil
	} else if len(hits) > 1 {
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.Name
		}
		slices.Sort(names)
		return "", nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("identifier %q matches multiple sessions (%s); use the session name", identifier, strings.Join(names, ", "))}
	}

	sessionName := identifier
	if cfg != nil {
		if disp, matched, err := dispatchResource(cfg, "", identifier); err == nil && matched {
			if session, err := store.GetE(disp.Name); err != nil {
				return "", nil, err
			} else if session != nil {
				return disp.Name, session, nil
			}
			sessionName = disp.Name
		}
	}

	return "", nil, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("no state entry for session %q", sessionName)}
}

// ResolveSession is the exported entry point for resolving an identifier
// (session name, create-time alias, or resource id) to a session, using the
// same lookup order as the internal resolver. It hands back the raw
// mutating-lifecycle-owned session, so a caller that mutates the session
// (e.g. to update its message) can call this directly, but a
// caller that only needs the canonical name should call ResolveSessionName
// instead, and one that needs read-only session fields should call a
// dedicated projection function instead of reading the raw struct.
func ResolveSession(cfg *config.Config, store *state.Store, identifier string) (string, *domain.Session, error) {
	return resolveSession(cfg, store, identifier)
}

// ResolveSessionName resolves an identifier to its canonical session name,
// using the same lookup order as ResolveSession, without handing back the
// raw session for callers that only need the name (e.g. `plect ls --parent`
// filtering entries by ParentSession).
func ResolveSessionName(cfg *config.Config, store *state.Store, identifier string) (string, error) {
	name, _, err := resolveSession(cfg, store, identifier)
	return name, err
}

// TaskInstanceView is the display projection of one task instance: its
// identity, scope, and status, plus — when the task declares a done_when —
// the per-instance evaluation against the instance's own outputs.
// Only dynamic instances and done_when-bearing tasks are projected; pure
// lifecycle-only static nodes (the runtime task, the agent launcher) are
// omitted to keep show/ls focused.
type TaskInstanceView struct {
	Instance          string                  `json:"instance"`
	TaskID            string                  `json:"task_id,omitempty"`
	Scope             string                  `json:"scope"`
	Status            string                  `json:"status"`
	IsTask            bool                    `json:"dynamic,omitempty"`
	Name              string                  `json:"name,omitempty"`
	Resource          string                  `json:"resource,omitempty"`
	DoneWhen          *task.DoneWhenResult    `json:"done_when,omitempty"`
	Finalized         bool                    `json:"finalized,omitempty"` // set once `plect task finalize` has recorded completion; cleanup still pending
	Outputs           map[string]any          `json:"outputs,omitempty"`
	PersistedDoneWhen *contract.DoneWhenState `json:"persisted_done_when,omitempty"`
}

// sessionTaskItem is the shared per-instance projection both taskViews
// (ls/List's legacy `tasks` display) and statusTaskViews (plect status's `work`
// layer) build from — instance identity, the dynamic-or-done_when filter, and
// the done_when evaluation itself live in exactly one place so the two
// display surfaces cannot silently drift apart.
type sessionTaskItem struct {
	seq       int
	instance  string
	taskID    string
	scope     string
	status    string
	dynamic   bool
	name      string
	resource  string
	outputs   map[string]any
	state     map[string]any
	observed  *contract.ResourceObservation
	doneWhen  *task.DoneWhenResult
	finalized bool
}

// sessionTaskItems projects a session's task instances, ordered by
// instantiation Seq so display matches the instantiation stack. defs supplies
// the done_when predicates (loaded from the trusted config layers); a nil defs
// degrades to dynamic-instance identity only. cached supplies a done_when
// result already evaluated elsewhere for the same (def, outputs, context) —
// e.g. evaluateSessionActions, which Status calls first — so this projection
// doesn't redundantly re-evaluate it; a cache miss (not produced, or no
// done_when-bearing evaluation ran) still evaluates it directly.
func sessionTaskItems(cfg *config.Config, declarations taskDeclarations, session *domain.Session, sessions map[string]*domain.Session, cached map[string]task.DoneWhenResult) []sessionTaskItem {
	if session == nil || (len(session.Nodes) == 0 && len(session.Tasks) == 0) {
		return nil
	}
	var items []sessionTaskItem
	// dynamic tags every item appendCollection builds from this call: false
	// for session.Nodes, true for session.Tasks — the same distinction the
	// pre-split TaskState.Dynamic field used to carry per entry.
	appendCollection := func(collection map[string]*contract.TaskState, dynamic bool) {
		for key, st := range collection {
			if st == nil {
				continue
			}
			taskID := taskIDForInstance(key, st)
			var dwResult *task.DoneWhenResult
			if r, ok := cached[key]; ok {
				rc := r
				dwResult = &rc
			} else if declarations.declares(taskID) {
				dw, live, err := declarations.gate(key, st)
				if err == nil && dw != nil {
					res := task.EvaluateTaskDoneWhenWithContext(dw, live, doneWhenEvalContext(session.Name, st, sessions))
					dwResult = &res
				}
			}
			if st.Status != contract.TaskStatusProduced && !dynamic && dwResult == nil {
				continue // not produced, not dynamically named, no done_when to report — nothing to show yet
			}
			items = append(items, sessionTaskItem{
				seq: st.Seq, instance: key, taskID: st.TaskID, scope: st.Scope, status: st.Status,
				dynamic: dynamic, name: st.Name, resource: st.Resource, outputs: st.Outputs,
				state: st.State, observed: st.Observed,
				doneWhen: dwResult, finalized: !st.FinalizedAt.IsZero(),
			})
		}
	}
	appendCollection(session.Nodes, false)
	appendCollection(session.Tasks, true)
	slices.SortStableFunc(items, func(a, b sessionTaskItem) int {
		if a.seq != b.seq {
			return a.seq - b.seq
		}
		return strings.Compare(a.instance, b.instance)
	})
	return items
}

// taskViews projects a session's task instances for display. See
// sessionTaskItems for the shared projection logic.
func taskViews(cfg *config.Config, declarations taskDeclarations, session *domain.Session, sessions map[string]*domain.Session) []TaskInstanceView {
	items := sessionTaskItems(cfg, declarations, session, sessions, nil)
	if items == nil {
		return nil
	}
	out := make([]TaskInstanceView, len(items))
	for i, it := range items {
		out[i] = TaskInstanceView{
			Instance:  it.instance,
			TaskID:    it.taskID,
			Scope:     it.scope,
			Status:    it.status,
			IsTask:    it.dynamic,
			Name:      it.name,
			Resource:  it.resource,
			DoneWhen:  it.doneWhen,
			Finalized: it.finalized,
		}
	}
	return out
}

// lookupTombstone resolves identifier to a session name the same way
// resolveSession does (but without requiring a live state entry — the state
// entry is exactly what's gone by the time a tombstone matters) and reads
// that session's tombstone from the event log, if any. A nil, nil result
// means no tombstone exists.
func lookupTombstone(cfg *config.Config, store *state.Store, identifier string) (*contract.Tombstone, error) {
	sessionName, err := resolveSessionName(cfg, store, identifier)
	if err != nil {
		return nil, err
	}
	return store.Tombstone(sessionName)
}

// childNames lists the sessions whose ParentSession is name, sorted. Derived
// from the parent pointers (the tree fact Subtree walks), not the Children
// slice, so the projection cannot drift from subtree membership.
func childNames(sessions map[string]*domain.Session, name string) []string {
	var out []string
	for n, s := range sessions {
		if s != nil && s.ParentSession == name {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// ListEntry represents a single entry in the session list.
type ListEntry struct {
	SessionName   string             `json:"session_name"`
	Revision      string             `json:"revision,omitempty"`
	Title         string             `json:"title,omitempty"`
	Run           domain.RunState    `json:"run"`
	Sleep         string             `json:"sleep,omitempty"`
	QueuePosition int                `json:"queue_position,omitempty"`
	Health        domain.HealthState `json:"health,omitempty"`
	HealthReason  string             `json:"health_reason,omitempty"`
	DisplayStatus string             `json:"display_status"`
	ResourceID    string             `json:"resource_id"`
	Tracked       bool               `json:"tracked"`
	LastActiveAt  *time.Time         `json:"last_active_at,omitempty"`
	Message       *domain.Message    `json:"message,omitempty"`
	Workdir       string             `json:"workdir,omitempty"`
	ParentSession string             `json:"parent_session,omitempty"`
	// Tasks projects the session's dynamic instances and done_when-bearing
	// tasks with their per-instance done_when status.
	Tasks []TaskInstanceView `json:"tasks,omitempty"`
}

// List returns all sessions with their statuses. Health is whatever the
// periodic healthcheck sweep already persisted — a listing must never
// itself run a probe.
func List(cfg *config.Config, store *state.Store) ([]ListEntry, error) {
	sessions, err := store.AllE()
	if err != nil {
		return nil, err
	}
	entries := make([]ListEntry, 0, len(sessions))
	for _, s := range sessions {
		declarations := taskDeclarations{}
		if s.WorkflowRevision != "" {
			docs, effects, err := cfg.TaskDeclarationsForSession(s)
			if err != nil {
				return nil, err
			}
			declarations = taskDeclarations{docs: docs, effects: effects}
		}
		entry, err := buildListEntry(cfg, store, declarations, s, sessions)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	// store.All ranges a map, so sort by name to make List deterministic —
	// callers (plect ls, MCP, web UI auto-refresh) get a stable order.
	slices.SortFunc(entries, func(a, b ListEntry) int {
		return strings.Compare(a.SessionName, b.SessionName)
	})

	return entries, nil
}

func buildListEntry(cfg *config.Config, store *state.Store, displayTasks taskDeclarations, s *domain.Session, sessions map[string]*domain.Session) (ListEntry, error) {
	var cached cachedInfo
	applyDisplay(s, &cached)

	run := sessionRunState(cfg, s)
	sleep, position, err := sleepVisibility(cfg, store, s)
	if err != nil {
		return ListEntry{}, err
	}
	health, healthReason := persistedHealth(s)
	if run != domain.RunUp {
		// The sweep skips a down session, so its last recorded verdict can
		// predate the shutdown by any amount; a down session has no verdict
		// at all, so a stale one must not survive into its row.
		health, healthReason = "", ""
	}
	entry := ListEntry{
		SessionName:   s.Name,
		Revision:      shortWorkflowRevision(s.WorkflowRevision),
		Title:         cached.Title,
		Run:           run,
		Sleep:         sleep,
		QueuePosition: position,
		Health:        health,
		HealthReason:  healthReason,
		DisplayStatus: cached.DisplayStatus,
		ResourceID:    s.ResourceID,
		Tracked:       true,
		LastActiveAt:  &s.UpdatedAt,
		Message:       LatestStatusMessage(store, s.Name),
		Workdir:       listWorkdir(cfg, s),
		ParentSession: s.ParentSession,
		Tasks:         taskViews(cfg, displayTasks, s, sessions),
	}

	return entry, nil
}

func shortWorkflowRevision(digest string) string {
	if len(digest) < 8 {
		return digest
	}
	return digest[:8]
}

// sessionRunState reports the "run" fact: whether a current-plan run-scoped
// task instance has produced.
func sessionRunState(cfg *config.Config, s *domain.Session) domain.RunState {
	if s != nil && cfg.RunScopeUp(s) {
		return domain.RunUp
	}
	return domain.RunDown
}

// persistedHealth reads the sweep's own record rather than evaluating
// anything; an unreached session's zero HealthState renders as absent via
// ListEntry's omitempty rather than a fabricated verdict.
func persistedHealth(s *domain.Session) (domain.HealthState, string) {
	if s == nil || s.Health == nil {
		return "", ""
	}
	return domain.HealthState(s.Health.LastState), s.Health.LastReason
}

// liveChildrenOf never itself runs a probe: Health is whatever the periodic
// healthcheck sweep already persisted.
func liveChildrenOf(cfg *config.Config, allSessions map[string]*domain.Session, name string) []LiveChild {
	var out []LiveChild
	for _, childName := range childNames(allSessions, name) {
		child := allSessions[childName]
		if sessionRunState(cfg, child) != domain.RunUp {
			continue
		}
		health, _ := persistedHealth(child)
		minutes := 0
		if !child.LastTickAt.IsZero() {
			minutes = int(time.Since(child.LastTickAt).Minutes())
		}
		out = append(out, LiveChild{
			Name:             childName,
			Run:              domain.RunUp,
			Health:           health,
			MinutesSinceTick: minutes,
		})
	}
	return out
}

func sessionHealthReport(cfg *config.Config, store *state.Store, name string) (HealthReport, domain.HealthState) {
	report, err := EvaluateHealth(cfg, store, name)
	if err != nil {
		return HealthReport{}, domain.HealthUnhealthy
	}
	return report, report.State()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// taskIDForInstance resolves a task instance's definition ID. Empty TaskID
// preserves the older static-node state shape.
func taskIDForInstance(key string, st *contract.TaskState) string {
	if st.TaskID != "" {
		return st.TaskID
	}
	return key
}

// cachedInfo holds a session's display projection (title, status line).
type cachedInfo struct {
	Title         string
	DisplayStatus string
}

// Workdir resolves an identifier to the directory its session's workflow
// declares as `workdir`, evaluated against the outputs its producer recorded.
// The directory is never stored: a session whose workflow declares none, or
// whose producer is not produced, has no workdir.
func Workdir(cfg *config.Config, store *state.Store, identifier string) (string, error) {
	if _, session, err := resolveSession(cfg, store, identifier); err == nil {
		dir, dirErr := sessionWorkdir(cfg, session)
		if dirErr != nil {
			return "", &Error{Code: ErrNotProduced, Message: fmt.Sprintf("session %q has no workdir: %v", session.Name, dirErr)}
		}
		if dir == "" {
			return "", &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q has no workdir: its workflow declares none", session.Name)}
		}
		return dir, nil
	} else if svcErr, ok := err.(*Error); ok && svcErr.Code != ErrSessionNotFound {
		// An ambiguous alias must surface rather than fall through.
		return "", err
	}

	return "", &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("no state entry for session %q", identifier)}
}

// listWorkdir is the listing's best-effort workdir: empty when none is declared
// or it cannot be evaluated yet, since a listing never fails on one session.
func listWorkdir(cfg *config.Config, s *domain.Session) string {
	dir, err := sessionWorkdir(cfg, s)
	if err != nil {
		return ""
	}
	return dir
}

// applyDisplay resolves the workflow's [display] values into cached when they
// produce a non-empty result. Evaluation is state-only (the workflow's own
// persisted outputs and the session's inputs) — no network. A value that does
// not resolve leaves the field at what it already shows: a listing reporting
// a blank title would read as a session that has none.
func applyDisplay(s *domain.Session, cached *cachedInfo) {
	if s.Workflow == "" {
		return
	}
	stored, ok, err := config.RevisionForSession(s)
	if err != nil || !ok {
		return
	}
	wf := stored.Workflow
	if len(wf.Display) == 0 {
		return
	}
	outputs := workflowDisplayOutputs(s)
	for _, shown := range []struct {
		key  string
		into *string
	}{
		{"title", &cached.Title},
		{"status", &cached.DisplayStatus},
	} {
		value, declared := wf.Display[shown.key]
		if !declared {
			continue
		}
		if resolved, err := task.ResolveDisplay(value, outputs, s.Inputs); err == nil && resolved != "" {
			*shown.into = resolved
		}
	}
}

func workflowDisplayOutputs(s *domain.Session) map[string]any {
	outputs, _ := publicoutput.Of(s, publicoutput.ProducedOnly)
	return outputs
}

// An empty report always appends because it is the only record of a fresh
// idle confirmation. A new turn also appends even when its text is unchanged.
func SetMessage(cfg *config.Config, store *state.Store, identifier string, text string, turnID *string) error {
	sessionName, _, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return guardErr
	}
	log := eventlog.NewStore(store.Dir())
	latest, ok, err := log.LatestByType(sessionName, event.TypeStatusMessage)
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	previous := ""
	if ok && latest.Metadata["cleared"] != "true" {
		previous = latest.Metadata["text"]
	}
	latestTurnID, latestHasTurnID := latest.Metadata["turn_id"]
	turnIDMatches := turnID == nil && !latestHasTurnID || turnID != nil && latestHasTurnID && *turnID == latestTurnID
	if text != "" && previous == text && ok && turnIDMatches {
		return nil
	}
	cleared := "false"
	if text == "" {
		cleared = "true"
	}
	metadata := map[string]string{
		"text":     text,
		"cleared":  cleared,
		"previous": previous,
	}
	if turnID != nil {
		metadata["turn_id"] = *turnID
	}
	if _, _, _, err := log.Append(event.Event{
		SessionName: sessionName,
		Type:        event.TypeStatusMessage,
		Source:      event.SourcePlect,
		Direction:   event.Outbound,
		Summary:     text,
		Metadata:    metadata,
	}); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	return nil
}
