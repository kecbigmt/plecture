// Package state defines the shared contract types for plect state.json.
//
// These types represent the boundary data that other components
// (slack-adapter) may read from state.json.
// plect owns and writes these; consumers read only.
package state

import (
	"encoding/json"
	"time"
)

// Conversation holds information about an external communication channel
// associated with a session (e.g., a Slack thread, Discord channel).
type Conversation struct {
	Source   string            `json:"source"`             // Display label: "Slack", "Discord", etc.
	URL      string            `json:"url"`                // Permalink to the conversation
	Metadata map[string]string `json:"metadata,omitempty"` // Plugin-specific data (thread_ts, channel_id, etc.)
}

// Message is a session-level, self-reported free-text status line (e.g. an
// agent's turn-boundary "working"/"waiting" self-report). plect does not
// interpret Text; it is a slot for external updaters, not a plect concept.
type Message struct {
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DoneWhenJudge is the reviewer-owned verdict for one done_when judge leaf.
// Revision is an opaque provider value; plect only compares it for exact equality
// with the instance's current revision output.
//
// The record is self-contained: TargetSession / Instance name the work it
// judges, and ReviewerWorkflow / Relation stamp the reviewer's workflow and the
// reviewer→target tree relation *as computed at record time*. Stamping rather
// than re-deriving means the verdict still reads correctly after the reviewer
// session is destroyed or the tree is restructured. Relation is the fact the
// verdict was made under; which relations a leaf accepts is a separate policy.
type DoneWhenJudge struct {
	LeafID           string    `json:"leaf_id"`
	Action           string    `json:"action"` // "approve" | "request_changes"
	Reason           string    `json:"reason"` // reviewer evidence
	Revision         string    `json:"revision"`
	TargetSession    string    `json:"target_session,omitempty"`
	Instance         string    `json:"instance,omitempty"`
	ReviewerSession  string    `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string    `json:"reviewer_workflow,omitempty"`
	Relation         string    `json:"relation,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitzero"`
}

// DoneWhenState is reviewer/checker-owned completion state for one task instance's
// done_when. It is separate from observed outputs so status/display can read
// completion state without causing dispatch, rollback, or shell-out work.
type DoneWhenState struct {
	Judges               map[string]*DoneWhenJudge `json:"judges,omitempty"`
	HeartbeatTicks       int                       `json:"heartbeat_ticks,omitempty"`
	HeartbeatEscalations int                       `json:"heartbeat_escalations,omitempty"`
	LastAction           string                    `json:"last_action,omitempty"`
	LastFingerprint      string                    `json:"last_fingerprint,omitempty"`
	LastReason           string                    `json:"last_reason,omitempty"`
	LastUnsatisfied      []string                  `json:"last_unsatisfied,omitempty"`
	LastBody             string                    `json:"last_body,omitempty"`
	EscalatedAt          time.Time                 `json:"escalated_at,omitzero"`
	EscalateReason       string                    `json:"escalate_reason,omitempty"`
}

// Task lifecycle status values for TaskState.Status. Task is a runtime
// entity; the status describes the entity's existence state, not the action
// last taken.
//
//   - produced — task is in place. Either setup succeeded, or the task
//     declares no setup so it is considered live from the start.
//   - failed   — setup or cleanup errored.
//   - cleaned  — task has been torn down. This is the terminal "gone"
//     state and applies whether the task declared a cleanup command or not.
const (
	TaskStatusProduced = "produced"
	TaskStatusFailed   = "failed"
	TaskStatusCleaned  = "cleaned"
)

// Task scope values for TaskState.Scope.
const (
	TaskScopeSession = "session"
	TaskScopeRun     = "run"
)

// WorkflowPseudoNodeID is the reserved Session.Tasks key for the
// workflow-level setup/cleanup pseudo-node. The "@" prefix is outside the
// node-id grammar ([A-Za-z_][A-Za-z0-9_]*), so it can never collide with a
// real workflow node.
const WorkflowPseudoNodeID = "@workflow"

// EnvironmentPseudoNodeID is the reserved Session.Tasks key for the
// environment-level setup/cleanup pseudo-node, mirroring
// WorkflowPseudoNodeID. A workflow that declares no environment (host
// degeneration) never creates this key.
const EnvironmentPseudoNodeID = "@environment"

// OutputKeyWorkdir is the reserved output key naming the session's working
// directory. It is always immutable: declaring it `mutable = true` in an
// outputs schema is a load error, and `plect state set-output` rejects it.
const OutputKeyWorkdir = "workdir"

// TaskState records the persisted outcome of a task's setup/cleanup.
// Runtime liveness is not stored here — it is determined on demand via healthcheck.
//
// Inputs/Outputs are paired and consistently plural — `Inputs` is the map of
// resolved input bindings persisted at setup time so cleanup can run without
// the original CLI invocation. Empty for legacy tasks with no input mapping.
//
// Seq / Dynamic / Resource support dynamic instantiation:
//
//   - Seq is the monotonically increasing instantiation order across every
//     task in the session (workflow pseudo-node, static DAG nodes, and
//     dynamic `plect task setup` instances). Teardown reclaims tasks in
//     descending Seq — the reverse of the single instantiation stack — so an
//     task always outlives anything that depends on it. Zero on legacy state
//     written before this field existed (such entries fall back to plan order).
//   - Dynamic marks instances created at runtime via `plect task setup` (as
//     opposed to static workflow DAG nodes). Dynamic instances are not in the
//     compiled plan, so teardown reconstructs their cleanup from the task
//     definition keyed by TaskID.
//   - Resource is the `--resource` value bound at instantiation (the external
//     resource this instance works on); empty for instances with no resource.
//   - Name is a `--name` instance identity for a dynamic instance: when set, the
//     instance key IS the name (session-global unique, no `<task>#` prefix), so
//     a second `setup --name <name>` collides. Empty for the numbered
//     `<task>#<n>` form. Shown by `plect status` / `ls`.
type TaskState struct {
	Scope         string          `json:"scope"`              // "session" | "run"
	TaskID        string          `json:"task_id,omitempty"`  // workflow node's `uses` target; omitted when node id == task id (legacy)
	Status        string          `json:"status"`             // "produced" | "failed" | "cleaned"
	Inputs        map[string]any  `json:"inputs,omitempty"`   // resolved node inputs (post-template), persisted for cleanup
	Outputs       map[string]any  `json:"outputs,omitempty"`  // parsed JSON from setup stdout
	Seq           int             `json:"seq,omitempty"`      // instantiation order; 0 = legacy/unset
	Dynamic       bool            `json:"dynamic,omitempty"`  // true for runtime `plect task setup` instances
	Resource      string          `json:"resource,omitempty"` // bound --resource at instantiation
	Name          string          `json:"name,omitempty"`     // --name instance identity (key == name when set)
	DoneWhen      *DoneWhenState  `json:"done_when,omitempty"`
	ExtraDoneWhen json.RawMessage `json:"extra_done_when,omitempty"`
	SetupAt       time.Time       `json:"setup_at,omitzero"`
	FailedAt      time.Time       `json:"failed_at,omitzero"`
	CleanedAt     time.Time       `json:"cleaned_at,omitzero"`
	FinalizedAt   time.Time       `json:"finalized_at,omitzero"` // set by `plect task finalize`; instance still awaits `plect task cleanup`
	Error         string          `json:"error,omitempty"`
}

// Session is the shared representation of a plect session in state.json.
// This contains all fields that external consumers may read.
//
// Workflow is the chosen workflow name (e.g. "coding-claude"). Frozen at create
// time so subsequent up/down/destroy use a stable plan regardless of config
// changes. Empty means the legacy inline-`[[tasks]]` path was used.
//
// ResourceID is the canonical external resource this session works on (any
// string — the provider that resolved it decides what the string means);
// Alias preserves the original create-time input so lookups survive resolver
// rule changes. Anything resource-shaped beyond those two (a repository, a
// number, a permalink) is a provider setup output, not a session field.
type Session struct {
	Name          string                `json:"session_name"`
	ResourceID    string                `json:"resource_id,omitempty"`
	ParentSession string                `json:"parent_session,omitempty"`
	Children      []string              `json:"children,omitempty"`
	Alias         string                `json:"alias,omitempty"`
	Branch        string                `json:"branch"`
	WorkdirPath   string                `json:"workdir_path"`
	Conversation  *Conversation         `json:"conversation,omitempty"`
	Message       *Message              `json:"message,omitempty"`
	Workflow      string                `json:"workflow,omitempty"`
	Inputs        map[string]any        `json:"inputs,omitempty"`
	Tasks         map[string]*TaskState `json:"tasks,omitempty"`
	// Health is the last healthcheck observation and movement fingerprint
	// core recorded for this session. It is persisted so stall judgment and
	// parent re-notification use one durable history instead of a caller's
	// transient poll cadence.
	Health *HealthState `json:"health,omitempty"`
	// LastTickAt is the session-level watermark `plect tick` stamps every time
	// it runs (regardless of instance/action outcome). The tick reactor's
	// `heartbeat` sweep reads it to decide whether observation has gone
	// stale; a tick always resets it, so a session with a live notification
	// path never accrues an extra sweep.
	LastTickAt time.Time `json:"last_tick_at,omitzero"`
	// TickBackoff is nil until the first heartbeat sweep decides a tick, so a
	// session that has never backed off keeps a clean state.json.
	TickBackoff *TickBackoff `json:"tick_backoff,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// HealthState is core's own record of the last opaque movement fingerprint,
// the time core last observed that fingerprint change, and the last rendered
// health judgment. LastMovementAt is core's own clock, not any timestamp a
// source script may report.
type HealthState struct {
	LastCheckedAt   time.Time `json:"last_checked_at,omitzero"`
	LastMovementAt  time.Time `json:"last_movement_at,omitzero"`
	LastFingerprint string    `json:"last_fingerprint,omitempty"`
	LastState       string    `json:"last_state,omitempty"`
	LastReason      string    `json:"last_reason,omitempty"`
	LastNotifiedAt  time.Time `json:"last_notified_at,omitzero"`
	NotifyCount     int       `json:"notify_count,omitempty"`
}

// TickBackoff is the quiet-tick exponential backoff bookkeeping the tick
// reactor's `heartbeat` sweep persists per session. It is
// session-scoped like LastTickAt, not per-instance, because a heartbeat
// sweep evaluates every produced instance of a session in one pass and the
// backoff applies to that combined observation.
type TickBackoff struct {
	// LastFingerprint is the composite done_when fingerprint (across every
	// instance) as of the last heartbeat sweep; a change resets ConsecutiveUnchanged.
	LastFingerprint string `json:"last_fingerprint,omitempty"`
	// LastLogPosition is the event-log byte offset up to which inbound events
	// have been scanned; an inbound event past it resets ConsecutiveUnchanged.
	LastLogPosition int64 `json:"last_log_position,omitempty"`
	// ConsecutiveUnchanged counts heartbeat sweeps in a row with neither a
	// fingerprint change nor an inbound event; interval = heartbeat * 2^n.
	ConsecutiveUnchanged int `json:"consecutive_unchanged,omitempty"`
}

// Tombstone is the durable snapshot a session leaves behind in its event log
// directory when `plect destroy` deletes its state.json entry. It embeds the
// full Session (resource mapping, task outputs, done_when/judge records) so
// that context survives destroy instead of being lost alongside the state
// entry.
type Tombstone struct {
	Session
	DestroyedAt time.Time `json:"destroyed_at"`
}

// StateFile is the top-level structure of state.json.
type StateFile struct {
	Version  int                 `json:"version"`
	Sessions map[string]*Session `json:"sessions"`
}
