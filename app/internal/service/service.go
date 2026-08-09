package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	gh "github.com/kecbigmt/plect/app/internal/github"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	"github.com/kecbigmt/plect/app/internal/workspace"
	contract "github.com/kecbigmt/plect/contracts/state"
)

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

// effectiveTag resolves the workspace-identity tag that becomes part of a
// session name. An explicit --tag wins; otherwise the workflow id is the
// default, so two tools acting on one resource (claude work, codex review)
// materialize distinct workspaces instead of racing for one branch/worktree.
// The tag is never empty on the provider-dispatch paths — workspace identity
// always carries a label.
func effectiveTag(tag, workflowID string) (string, *Error) {
	if tag != "" {
		if err := validateTagFormat(tag); err != nil {
			return "", err
		}
		return tag, nil
	}
	if err := validateTagFormat(workflowID); err != nil {
		return "", &Error{Code: ErrInvalidTag, Message: fmt.Sprintf("workflow id %q cannot seed a workspace tag (must match [a-zA-Z0-9_-]+); pass --tag explicitly", workflowID)}
	}
	return workflowID, nil
}

// resolveSession resolves an identifier (session name, create-time alias, or
// resource id) to a session from the store. Lookup order:
//
//  1. exact session name
//  2. create-time alias (survives resolver rule changes; ambiguous when tag
//     variants share the alias)
//  3. legacy GitHub URL → owner/repo-<number> derivation
//  4. resolver derivation (pure, offline — works during provider outages)
func resolveSession(cfg *config.Config, store *state.Store, identifier string) (string, *domain.Session, error) {
	if session := store.Get(identifier); session != nil {
		return identifier, session, nil
	}

	if hits := store.FindByAlias(identifier); len(hits) == 1 {
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
	if gh.IsURL(identifier) {
		parsed, err := gh.ParseURL(identifier)
		if err != nil {
			return "", nil, &Error{Code: ErrInvalidURL, Message: err.Error()}
		}
		sessionName = gh.SessionName(parsed.OwnerRepo, parsed.Number)
		if session := store.Get(sessionName); session != nil {
			return sessionName, session, nil
		}
	}

	if cfg != nil {
		if disp, matched, err := dispatchResource(cfg, "", identifier); err == nil && matched {
			if session := store.Get(disp.Name); session != nil {
				return disp.Name, session, nil
			}
			sessionName = disp.Name
		}
	}

	return "", nil, &Error{Code: ErrWorkspaceNotFound, Message: fmt.Sprintf("no state entry for session %q", sessionName)}
}

// ResolveSession is the exported entry point for resolving an identifier
// (session name, create-time alias, or resource id) to a session, using the
// same lookup order as the internal resolver. Commands that need the raw
// session (e.g. `tws template render --session`) call this.
func ResolveSession(cfg *config.Config, store *state.Store, identifier string) (string, *domain.Session, error) {
	return resolveSession(cfg, store, identifier)
}

// TaskInstanceView is the display projection of one task instance: its
// identity, scope, and status, plus — when the task declares a done_when —
// the per-instance evaluation against the instance's own outputs (ADR-003).
// Only dynamic instances and done_when-bearing tasks are projected; pure
// lifecycle-only static nodes (the runtime task, the agent launcher) are
// omitted to keep show/ls focused.
type TaskInstanceView struct {
	Instance          string                  `json:"instance"`
	TaskID            string                  `json:"task_id,omitempty"`
	Scope             string                  `json:"scope"`
	Status            string                  `json:"status"`
	Dynamic           bool                    `json:"dynamic,omitempty"`
	Name              string                  `json:"name,omitempty"`
	Resource          string                  `json:"resource,omitempty"`
	DoneWhen          *task.DoneWhenResult    `json:"done_when,omitempty"`
	Finalized         bool                    `json:"finalized,omitempty"` // set once `tws task finalize` has recorded completion; cleanup still pending
	Outputs           map[string]any          `json:"outputs,omitempty"`
	PersistedDoneWhen *contract.DoneWhenState `json:"persisted_done_when,omitempty"`
}

// sessionTaskItem is the shared per-instance projection both taskViews
// (ls/List's legacy `tasks` display) and statusTaskViews (tws status's `work`
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
func sessionTaskItems(defs map[string]config.TaskDefinition, session *domain.Session, sessions map[string]*domain.Session, cached map[string]task.DoneWhenResult) []sessionTaskItem {
	if session == nil || len(session.Tasks) == 0 {
		return nil
	}
	var items []sessionTaskItem
	for key, st := range session.Tasks {
		if st == nil {
			continue
		}
		if key == contract.WorkflowPseudoNodeID {
			// The workflow pseudo-node carries session-level outputs (title,
			// branch, ...) but no done_when or lifecycle status of its own —
			// still one Task line + outputs, per the unified display rule.
			if len(st.Outputs) > 0 {
				items = append(items, sessionTaskItem{seq: st.Seq, instance: key, outputs: st.Outputs})
			}
			continue
		}
		taskID := taskIDForInstance(key, st)
		var dwResult *task.DoneWhenResult
		if r, ok := cached[key]; ok {
			rc := r
			dwResult = &rc
		} else if def, ok := defs[taskID]; ok {
			dw, err := effectiveDoneWhen(def.DoneWhen, st)
			if err == nil && dw != nil {
				res := task.EvaluateTaskDoneWhenWithContext(dw, st.Outputs, doneWhenEvalContext(session.Name, st, sessions))
				dwResult = &res
			}
		}
		if st.Status != contract.TaskStatusProduced && !st.Dynamic && dwResult == nil {
			continue // not produced, not dynamically named, no done_when to report — nothing to show yet
		}
		items = append(items, sessionTaskItem{
			seq: st.Seq, instance: key, taskID: st.TaskID, scope: st.Scope, status: st.Status,
			dynamic: st.Dynamic, name: st.Name, resource: st.Resource, outputs: st.Outputs,
			doneWhen: dwResult, finalized: !st.FinalizedAt.IsZero(),
		})
	}
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
func taskViews(defs map[string]config.TaskDefinition, session *domain.Session, sessions map[string]*domain.Session) []TaskInstanceView {
	items := sessionTaskItems(defs, session, sessions, nil)
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
			Dynamic:   it.dynamic,
			Name:      it.name,
			Resource:  it.resource,
			DoneWhen:  it.doneWhen,
			Finalized: it.finalized,
		}
	}
	return out
}

// loadDisplayTasks loads the trusted-layer task definitions once for
// done_when display across sessions. Task definitions are trusted-layer-only
// (the workdir layer cannot contribute shell), so the worktree-independent load
// is sufficient — mirroring loadDisplayWorkflows.
func loadDisplayTasks(cfg *config.Config) map[string]config.TaskDefinition {
	if cfg == nil {
		return nil
	}
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		return nil
	}
	return defs
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
	data, ok, err := eventlog.NewStore(store.Dir()).ReadTombstone(sessionName)
	if err != nil || !ok {
		return nil, err
	}
	var tomb contract.Tombstone
	if err := json.Unmarshal(data, &tomb); err != nil {
		return nil, fmt.Errorf("tombstone: unmarshal: %w", err)
	}
	return &tomb, nil
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
	Title         string             `json:"title,omitempty"`
	Run           domain.RunState    `json:"run"`
	Health        domain.HealthState `json:"health,omitempty"`
	GitHubStatus  string             `json:"github_status"`
	LinkedPR      *LinkedPRInfo      `json:"linked_pr,omitempty"`
	URL           string             `json:"url"`
	Tracked       bool               `json:"tracked"`
	LastActiveAt  *time.Time         `json:"last_active_at,omitempty"`
	Message       *domain.Message    `json:"message,omitempty"`
	GitDirty      *bool              `json:"git_dirty,omitempty"`
	Branch        string             `json:"branch,omitempty"`
	WorktreePath  string             `json:"worktree_path,omitempty"`
	ParentSession string             `json:"parent_session,omitempty"`
	// Tasks projects the session's dynamic instances and done_when-bearing
	// tasks with their per-instance done_when status (ADR-003).
	Tasks []TaskInstanceView `json:"tasks,omitempty"`
}

// List returns all sessions with their statuses.
func List(cfg *config.Config, store *state.Store) ([]ListEntry, error) {
	sessions := store.All()
	cache := ghcache.NewCacheStore("").Load()
	displayWorkflows := loadDisplayWorkflows(cfg)
	displayTasks := loadDisplayTasks(cfg)

	// Each session may need healthcheck and git subprocesses; GitHub data comes
	// from the on-disk cache loaded above. Doing 50+ serially is the dominant
	// cost, so fan out over a bounded pool and fill a preallocated slice by
	// index; the sort below restores order.
	tracked := make([]*domain.Session, 0, len(sessions))
	for _, s := range sessions {
		tracked = append(tracked, s)
	}
	entries := make([]ListEntry, len(tracked))
	sem := make(chan struct{}, listConcurrency)
	var wg sync.WaitGroup
	for i := range tracked {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			entries[i] = buildListEntry(cfg, store, displayWorkflows, displayTasks, cache, tracked[i], sessions)
		}(i)
	}
	wg.Wait()

	// store.All ranges a map, so sort by name to make List deterministic —
	// callers (tws ls, MCP, web UI auto-refresh) get a stable order.
	slices.SortFunc(entries, func(a, b ListEntry) int {
		return strings.Compare(a.SessionName, b.SessionName)
	})

	return entries, nil
}

// listConcurrency bounds the per-session healthcheck/git fan-out in List.
const listConcurrency = 16

// buildListEntry gathers one session's runtime status. Safe to call
// concurrently: it only reads the shared cache/workflows and spawns its own
// subprocesses.
func buildListEntry(cfg *config.Config, store *state.Store, displayWorkflows map[string]config.WorkflowFile, displayTasks map[string]config.TaskDefinition, cache *ghcache.CacheFile, s *domain.Session, sessions map[string]*domain.Session) ListEntry {
	cached := lookupCache(cache, s)
	applyDisplay(displayWorkflows, cache, s, &cached)

	entry := ListEntry{
		SessionName:   s.Name,
		Title:         cached.Title,
		Run:           sessionRunState(s),
		Health:        sessionHealthState(cfg, store, s.Name),
		GitHubStatus:  cached.GitHubStatus,
		LinkedPR:      cached.LinkedPR,
		URL:           s.URL,
		Tracked:       true,
		LastActiveAt:  &s.UpdatedAt,
		Message:       s.Message,
		Branch:        s.Branch,
		WorktreePath:  s.WorktreePath,
		ParentSession: s.ParentSession,
		Tasks:         taskViews(displayTasks, s, sessions),
	}

	// Git dirty state, only if the worktree exists.
	if s.WorktreePath != "" && fileExists(s.WorktreePath) {
		if gitStatus, err := workspace.GetWorktreeStatus(s.WorktreePath); err == nil {
			dirty := gitStatus.Dirty || gitStatus.UntrackedFiles > 0
			entry.GitDirty = &dirty
		}
	}
	return entry
}

// sessionRunState reports the "run" fact: whether any run-scoped task
// instance has produced.
func sessionRunState(s *domain.Session) domain.RunState {
	if s != nil && runScopeUp(s.Tasks) {
		return domain.RunUp
	}
	return domain.RunDown
}

// sessionHealthState reports the "health" fact: the declared-healthcheck
// evaluation, independent of run state. An evaluation error surfaces as
// unhealthy — the same treatment the old three-value collapse gave it.
func sessionHealthState(cfg *config.Config, store *state.Store, name string) domain.HealthState {
	report, err := EvaluateHealth(cfg, store, name)
	if err != nil {
		return domain.HealthUnhealthy
	}
	return report.State()
}

func conversationJSON(conv *domain.Conversation) string {
	if conv == nil {
		return ""
	}
	b, err := json.Marshal(conv)
	if err != nil {
		return ""
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LinkedPRInfo holds summary information about a PR linked to an issue session.
type LinkedPRInfo struct {
	Number         int    `json:"number"`
	Title          string `json:"title,omitempty"`
	State          string `json:"state"`
	ReviewDecision string `json:"review_decision,omitempty"`
	ChecksStatus   string `json:"checks_status,omitempty"`
	URL            string `json:"url,omitempty"`
}

// cachedInfo holds GitHub information extracted from the cache for display.
type cachedInfo struct {
	Title          string
	GitHubStatus   string
	ReviewDecision string
	ChecksStatus   string
	CacheAge       string
	LinkedPR       *LinkedPRInfo
}

// lookupCache reads cached GitHub data for a session from a pre-loaded cache.
func lookupCache(cache *ghcache.CacheFile, session *domain.Session) cachedInfo {
	key := ghcache.CacheKey(session.OwnerRepo, session.Number)

	if session.URLType == string(gh.URLTypePR) {
		if entry, ok := cache.PullRequests[key]; ok {
			ghStatus := entry.State
			if entry.ReviewDecision != "" {
				ghStatus += " (" + FormatReviewDecision(entry.ReviewDecision) + ")"
			}
			return cachedInfo{
				Title:          entry.Title,
				GitHubStatus:   ghStatus,
				ReviewDecision: entry.ReviewDecision,
				ChecksStatus:   entry.ChecksStatus,
				CacheAge:       formatCacheAge(entry.FetchedAt),
			}
		}
	} else {
		if entry, ok := cache.Issues[key]; ok {
			info := cachedInfo{
				Title:        entry.Title,
				GitHubStatus: entry.State,
				CacheAge:     formatCacheAge(entry.FetchedAt),
			}
			if entry.LinkedPR != nil {
				info.LinkedPR = &LinkedPRInfo{
					Number:         entry.LinkedPR.Number,
					Title:          entry.LinkedPR.Title,
					State:          entry.LinkedPR.State,
					ReviewDecision: entry.LinkedPR.ReviewDecision,
					ChecksStatus:   entry.LinkedPR.ChecksStatus,
					URL:            fmt.Sprintf("https://github.com/%s/pull/%d", session.OwnerRepo, entry.LinkedPR.Number),
				}
			}
			return info
		}
	}
	return cachedInfo{}
}

// Workdir resolves an identifier to the session's working directory using
// the full state v3 lookup order (name → alias → legacy URL derivation →
// resolver derivation). For GitHub URLs with no state entry, it falls back
// to the legacy path-convention computation (gh branch resolve) so
// `tws cd <url>` keeps working for worktrees that predate state tracking.
func Workdir(cfg *config.Config, store *state.Store, identifier string) (string, error) {
	if _, session, err := resolveSession(cfg, store, identifier); err == nil {
		if session.WorktreePath == "" {
			return "", &Error{Code: ErrWorkspaceNotFound, Message: fmt.Sprintf("session %q has no working directory recorded", session.Name)}
		}
		return session.WorktreePath, nil
	} else if svcErr, ok := err.(*Error); ok && svcErr.Code != ErrWorkspaceNotFound {
		// Ambiguous alias / invalid URL — surface, don't fall through.
		return "", err
	}

	if gh.IsURL(identifier) {
		parsed, err := gh.ParseURL(identifier)
		if err != nil {
			return "", &Error{Code: ErrInvalidURL, Message: err.Error()}
		}
		mgr := workspace.NewManager(cfg.WorktreesRoot)
		branch, err := mgr.ResolveBranch(parsed)
		if err != nil {
			return "", &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		return mgr.WorktreePath(parsed.OwnerRepo, branch), nil
	}

	return "", &Error{Code: ErrWorkspaceNotFound, Message: fmt.Sprintf("no state entry for session %q", identifier)}
}

// loadDisplayWorkflows loads the trusted base layers once for [display]
// lookup across sessions. Display is a trusted-layer-only field, so the
// per-session ancestor overlays can't change it; one load serves the whole
// listing.
func loadDisplayWorkflows(cfg *config.Config) map[string]config.WorkflowFile {
	if cfg == nil {
		return nil
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return nil
	}
	return workflows
}

// applyDisplay overrides the ghcache-derived display values with the
// workflow's [display] templates when they render non-empty. Evaluation is
// state-only (pseudo-node outputs + the transitional ghcache shim) — no
// network.
func applyDisplay(workflows map[string]config.WorkflowFile, cache *ghcache.CacheFile, s *domain.Session, cached *cachedInfo) {
	if s.Workflow == "" {
		return
	}
	wf, ok := workflows[s.Workflow]
	if !ok || len(wf.Display) == 0 {
		return
	}
	outputs := workflowDisplayOutputs(s, cache)
	if expr, ok := wf.Display["title"]; ok {
		if v, err := task.RenderOutputsTemplate(expr, outputs, s.Tasks); err == nil && v != "" {
			cached.Title = v
		}
	}
	if expr, ok := wf.Display["status"]; ok {
		if v, err := task.RenderOutputsTemplate(expr, outputs, s.Tasks); err == nil && v != "" {
			cached.GitHubStatus = v
		}
	}
}

func workflowDisplayOutputs(s *domain.Session, cache *ghcache.CacheFile) map[string]any {
	out := map[string]any{}
	if ws, ok := s.Tasks[contract.WorkflowPseudoNodeID]; ok && ws != nil {
		maps.Copy(out, ws.Outputs)
	}
	key := ghcache.CacheKey(s.OwnerRepo, s.Number)
	if s.URLType == string(gh.URLTypePR) {
		if e, ok := cache.PullRequests[key]; ok {
			out["title"] = e.Title
			out["pr_state"] = e.State
			out["checks_status"] = e.ChecksStatus
			out["review_decision"] = e.ReviewDecision
		}
	} else if e, ok := cache.Issues[key]; ok {
		out["title"] = e.Title
		out["issue_state"] = e.State
		if e.LinkedPR != nil {
			out["pr_state"] = e.LinkedPR.State
			out["checks_status"] = e.LinkedPR.ChecksStatus
			out["review_decision"] = e.LinkedPR.ReviewDecision
		}
	}
	return out
}

// FormatReviewDecision converts a GitHub review decision (e.g. "CHANGES_REQUESTED")
// to a human-readable form (e.g. "changes requested").
func FormatReviewDecision(rd string) string {
	return strings.ToLower(strings.ReplaceAll(rd, "_", " "))
}

func formatCacheAge(fetchedAt time.Time) string {
	if fetchedAt.IsZero() {
		return ""
	}
	d := time.Since(fetchedAt)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// SetConversation updates the Conversation field of an existing session.
func SetConversation(cfg *config.Config, store *state.Store, identifier string, conv *domain.Conversation) error {
	sessionName, session, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return guardErr
	}
	session.Conversation = conv
	session.UpdatedAt = time.Now()
	return store.Put(session)
}

// SetMessage updates the session-level self-reported status message. An empty
// text unsets it (Session.Message becomes nil) rather than persisting a
// blank, since a blank line would look identical to "message never set" in
// display but consume an object in state.json.
func SetMessage(cfg *config.Config, store *state.Store, identifier string, text string) error {
	sessionName, session, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return guardErr
	}
	now := time.Now()
	if text == "" {
		session.Message = nil
	} else {
		session.Message = &domain.Message{Text: text, UpdatedAt: now}
	}
	session.UpdatedAt = now
	return store.Put(session)
}
