package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/sennit/app/internal/domain"
)

// WorkflowFile is loaded from `.sennit/workflows/<id>.toml` (per-repo) or from
// `~/.config/sennit/workflows/<id>.toml` (global). Per-repo wins on id conflict.
//
// A workflow is a named bundle of nodes. Each node selects a task definition
// via `uses` and binds its inputs as Go templates. The setup/cleanup DAG is
// derived from `.Nodes.<id>.outputs.<key>` references in those bindings, so
// `depends_on` is no longer part of the node surface.
//
// ID is derived from the filename stem and is *not* read from TOML — the
// filename is the single source of truth (renaming a workflow is `mv`, not
// `mv` + TOML edit). `name` is the human-readable display label (separate from
// identity); `description` is a short summary used by `sennit workflow list/show`
// and the MCP discovery tools.
type WorkflowFile struct {
	ID          string `toml:"-"`
	Name        string `toml:"name"`
	Description string `toml:"description"`
	// Provider names the resource provider (providers/<id>.toml in the
	// trusted base layers) this workflow runs on. The provider owns the
	// resource-kind knowledge — resolver, workdir setup/cleanup, the
	// @workflow outputs contract; the workflow owns the task shape on top
	// (nodes, inputs, done_when, display). A workflow without one cannot
	// acquire a working directory, so it cannot back a session.
	Provider string `toml:"provider"`
	// Environment names the environment definition (environments/<id>.toml in
	// the trusted base layers) this workflow's task Executor runs in. Empty
	// or the built-in "host" value are the same thing — no environment
	// lifecycle, tasks run directly on this machine (host degeneration) — so
	// declaring `environment = "host"` explicitly changes nothing observable.
	// Any other value is resolved against LoadEnvironments and its setup runs
	// after provider setup, before session task setup (see EnvironmentConfig);
	// a node's own `execution` decides whether it actually runs there.
	Environment string `toml:"environment"`
	// EnvironmentInputs is a passthrough table for values a non-host
	// Environment needs (e.g. a Docker image/tag). Core does not interpret
	// its contents, the same way TaskDefinition inputs pass through
	// untouched to setup/cleanup templates.
	//
	// TOML cannot nest a table under the same key as the `environment`
	// scalar above (`[environment.inputs]` would conflict with
	// `environment = "..."`), so this is its own top-level table:
	//
	//	environment = "docker"
	//	[environment_inputs]
	//	image = "myimage:latest"
	EnvironmentInputs map[string]any `toml:"environment_inputs"`
	// Display declares the values shown by `sennit ls` / `show` / the web UI as
	// templates over the session's persisted outputs:
	//
	//	[display]
	//	title  = "{{.Workflow.outputs.title}}"
	//	status = "{{.Workflow.outputs.pr_state}}"
	//
	// Evaluation reads state.json only — no network; freshness follows the
	// watcher's update cadence.
	Display    map[string]string `toml:"display"`
	AutoSelect *bool             `toml:"auto_select"`
	Nodes      []WorkflowNode    `toml:"nodes"`
	Event      WorkflowEvent     `toml:"event"`
	// Tick declares the machine-driven conditions that tick the sessions this
	// workflow produces (docs/wiki/verification-gate.md). Unlike the other
	// WorkflowFile fields, a deeper cascade layer's `[tick]` replaces a
	// shallower layer's wholesale (see mergeWorkflowLayers) — the same
	// deeper-wins policy as TaskDefinition, not the additive/no-redeclare
	// policy the rest of this struct uses. Nil means no declaration: the
	// session advances only via manual `sennit tick` and the judge builtin.
	Tick             *TickConfig    `toml:"tick"`
	InputsSchema     map[string]any `toml:"inputs_schema"`
	InputsSchemaFile string         `toml:"inputs_schema_file"`
	// BaseDir anchors InputsSchemaFile (and node-relative paths) so the
	// resolved location is independent of where the workflow file lives.
	BaseDir    string `toml:"-"`
	SourcePath string `toml:"-"`
}

// TickConfig declares when the tick reactor (internal/reactor) ticks a
// session produced by this workflow, on top of the judge builtin trigger
// (docs/wiki/verification-gate.md). Both fields are optional and independent:
// On alone is pure reactive tick; Heartbeat alone is pure periodic refresh;
// both together means notification-driven with a heartbeat backstop; neither
// means manual `sennit tick` and the judge builtin are the only drivers.
//
//	[tick]
//	on        = ["resource.*"] # event type globs; a match ticks the session
//	heartbeat = "15m"          # tick if this long has passed since the last tick
//
// On deliberately carries no provider-shaped default in core (any one
// watcher's event types are just one possible value): which event namespaces
// mean "an external resource changed" is a workflow-configuration concern,
// the same way wiring that watcher's channel is.
type TickConfig struct {
	On        []string `toml:"on"`
	Heartbeat Duration `toml:"heartbeat"`
	// MaxHeartbeat caps the quiet-tick exponential backoff interval
	// (heartbeat * 2^n) the reactor applies when consecutive heartbeat sweeps
	// see no fingerprint change and no inbound event. Zero means the
	// reactor's default (4h) applies — declaring it is optional.
	MaxHeartbeat Duration `toml:"max_heartbeat"`
	// ProgressSource declares a session-scoped dynamic output (the same
	// script-execution plumbing task.FetchOutput gives task-instance
	// outputs) whose fetched value core treats as an opaque progress
	// fingerprint (docs/wiki/verification-gate.md). Core never interprets
	// what the fingerprint string means or how it was produced — it only
	// compares the fetched value against the last one it persisted for this
	// session. Nil means no progress source is declared for this workflow.
	ProgressSource *DynamicOutput `toml:"progress_source"`
}

// WorkflowNode is a single instantiation of a task definition within a
// workflow. ID is the state key (independent of TaskID = `uses`) and Inputs
// is the template-string mapping fed to the task's setup/cleanup as `.Input.*`.
//
// Blocks declares reverse dependency edges: each listed node id becomes a
// dependent of this one, equivalent to writing the inverse dependency in the
// listed node. Use it when a cascade overlay needs to insert itself ahead of
// base nodes it cannot modify directly (e.g. a worktree-process killer that
// must clean up after the runtime/agent tasks).
type WorkflowNode struct {
	ID     string            `toml:"id"`
	Uses   string            `toml:"uses"`
	Inputs map[string]string `toml:"inputs"`
	Blocks []string          `toml:"blocks"`
}

// WorkflowEvent wraps [[event.channel]] in an [event] table so event-level
// settings can be added later without reshaping the workflow surface.
type WorkflowEvent struct {
	Channel []EventChannel `toml:"channel"`
}

// EventChannel selects a channel definition (channels/<uses>.toml) instead of
// a task and adds an `include` allowlist. Entries are event-type globs using the
// contracts/event MatchType rules. Inputs are template strings rendered against
// the session's node outputs at delivery.
//
//	[[event.channel]]
//	name = "runtime"
//	uses = "claude_channel"
//	inputs.path = "{{.Nodes.claude.outputs.socket_path}}"
//	include = ["sennit.instruction", "resource.*", "user.emit"]
type EventChannel struct {
	Name    string            `toml:"name"`
	Uses    string            `toml:"uses"`
	Inputs  map[string]string `toml:"inputs"`
	Include []string          `toml:"include"`
}

// TaskDefinition is loaded from `.sennit/tasks/<id>.toml` (per-repo) or
// `~/.config/sennit/tasks/<id>.toml` (global). Task definitions are reusable —
// multiple workflows may reference the same definition via `uses`, and a single
// workflow may instantiate the same definition under different node ids.
//
// ID is derived from the filename stem and is *not* read from TOML — the
// filename is the single source of truth (renaming a task is `mv`, not
// `mv` + TOML edit).
//
// Definitions intentionally have no `depends_on`: wiring is the workflow's job.
type TaskDefinition struct {
	ID          string `toml:"-"`
	Scope       string `toml:"scope"`
	Setup       string `toml:"setup"`
	Cleanup     string `toml:"cleanup"`
	Healthcheck string `toml:"healthcheck"`
	// ProgressSignal declares a provider-neutral command that reports opaque
	// progress facts as JSON on stdout: {"supported": bool,
	// "progress_expected": bool, "fingerprint": string, "observed_at":
	// RFC3339 string}. Core never interprets what the command actually
	// checked (a terminal pane, an agent transcript, a VCS worktree, ...) —
	// it only compares the fingerprint and timestamp the command reports.
	// Empty means no progress signal is declared for this task; a command
	// that runs but reports "supported": false is an explicit declaration
	// that this instance has no basis to judge progress right now, which
	// evaluates the same as if nothing were declared.
	ProgressSignal string   `toml:"progress_signal"`
	Primary        bool     `toml:"primary"`
	IdleAfter      Duration `toml:"idle_after"`
	Attach         string   `toml:"attach"`
	// Execution selects the execution plane for this task's setup/cleanup:
	// "host" or "environment". Empty defaults to the workflow's own
	// Environment (environment when declared, host otherwise) — see
	// task.ResolveExecution. Declaring "environment" on a workflow with no
	// Environment is a compile-time error, not a silent fallback to host.
	Execution string `toml:"execution"`
	// Capture declares a read-only template that snapshots what the task's
	// channel currently shows, declared on the same task that declares
	// attach (see config/sennit/tasks/tmux.toml, the built-in runtime task).
	// Symmetric with Attach — attach
	// hands the terminal over, capture only reads it — so the channel's own
	// identity stays inside the task definition; core never references it.
	Capture           string         `toml:"capture"`
	OutputsSchema     map[string]any `toml:"outputs_schema"`
	OutputsSchemaFile string         `toml:"outputs_schema_file"`
	InputsSchema      map[string]any `toml:"inputs_schema"`
	InputsSchemaFile  string         `toml:"inputs_schema_file"`
	// DoneWhen is the task's Definition of Done: a structured
	// completion predicate evaluated per instance. Nil for pure lifecycle-only
	// tasks (the runtime task, the agent launcher, chat notifications); set for
	// work units (work / review /
	// investigate).
	DoneWhen *DoneWhen `toml:"done_when"`
	// Requires names the output keys the task's done_when reads. It is the
	// explicit contract between the done_when check leaves and the outputs
	// populated by setup, explicit set-output, or dynamic output refresh. When
	// declared, every done_when check must name a required output, and every
	// required output must be an outputs schema property — so a typo in either
	// surfaces at compile time.
	Requires []string `toml:"requires"`
	// DynamicOutputs are outputs whose value is a script's stdout, fetched when
	// done_when's check reads them (distinct from the static OutputsSchema).
	DynamicOutputs []DynamicOutput `toml:"outputs"`
	// Chains declares the deterministic workflow-chaining rules that fire off
	// this task's instances: "once this task reaches this state, spawn that
	// workflow." Declared alongside DoneWhen (a separate section, not a
	// separate file) because a chain's `when` judge ids and `inputs` output
	// bindings are references into this same task's done_when/outputs_schema —
	// colocation is what lets validateTaskChains check those references at
	// load time instead of at fire time. Evaluation is scoped the same way:
	// a chain only ever fires against instances of the task that declared it.
	Chains     []ChainDefinition `toml:"chains"`
	BaseDir    string            `toml:"-"`
	SourcePath string            `toml:"-"`
}

// DoneWhen is the structured completion predicate a task definition may
// declare. It is a conjunction of leaves (All) plus an optional Budget.
//
// done_when has two leaf kinds: check leaves compare observed outputs, while
// judge leaves wait for independent reviewer input recorded by `sennit judge`.
type DoneWhen struct {
	All    []DoneWhenLeaf `toml:"all" json:"all"`
	Budget map[string]any `toml:"budget" json:"budget,omitempty"`
}

// DoneWhenLeaf is one conjunct of a done_when. It is either a check leaf or a
// judge leaf, exclusively.
//
//		all = [
//		  { check = "checks_status", eq = "SUCCESS" },
//		  { check = "coverage", gte = 80 },
//		  { check = "pr_state", in = ["merged", "closed"] },
//		  { judge = "AC satisfied with reasoning", id = "ac-met" },
//		]
//
//	  - A check leaf names an output (Check) and applies exactly one comparison
//	    operator to that output's value: Eq / Ne (string equality), In
//	    (membership), or Gte / Lte (numeric). Operators are typed in TOML, so a
//	    non-numeric gte/lte is a decode error.
//	  - A judge leaf carries a free-text criterion (Judge) and an optional id.
//	    Relation is the reviewer→work tree relations the leaf accepts a verdict
//	    from (default sibling/parent); self is always rejected, so independence is
//	    structural. Which reviewer workflow runs is a chaining concern, not a leaf
//	    field.
type DoneWhenLeaf struct {
	Check    string   `toml:"check" json:"check,omitempty"`
	Eq       *string  `toml:"eq" json:"eq,omitempty"`
	Ne       *string  `toml:"ne" json:"ne,omitempty"`
	In       []any    `toml:"in" json:"in,omitempty"`
	Gte      *float64 `toml:"gte" json:"gte,omitempty"`
	Lte      *float64 `toml:"lte" json:"lte,omitempty"`
	Judge    string   `toml:"judge" json:"judge,omitempty"`
	ID       string   `toml:"id" json:"id,omitempty"`
	Relation []string `toml:"relation" json:"relation,omitempty"`
}

// operatorCount returns how many comparison operators the leaf sets.
func (l DoneWhenLeaf) operatorCount() int {
	n := 0
	if l.Eq != nil {
		n++
	}
	if l.Ne != nil {
		n++
	}
	if l.In != nil {
		n++
	}
	if l.Gte != nil {
		n++
	}
	if l.Lte != nil {
		n++
	}
	return n
}

// Validate checks the structural invariants of a done_when: it must declare at
// least one leaf, a check leaf must name an output and set exactly one operator,
// a judge leaf must set no operators, and the two leaf kinds are exclusive.
// Operator value typing (e.g. gte must be numeric) is enforced at TOML decode by
// the field types.
func (d *DoneWhen) Validate() error {
	if d == nil {
		return nil
	}
	if len(d.All) == 0 {
		return fmt.Errorf("done_when declares no leaves; add at least one `all` entry or remove the table")
	}
	for i, leaf := range d.All {
		hasCheck := strings.TrimSpace(leaf.Check) != ""
		hasJudge := strings.TrimSpace(leaf.Judge) != ""
		ops := leaf.operatorCount()
		switch {
		case hasCheck && hasJudge:
			return fmt.Errorf("done_when.all[%d] sets both `check` and `judge`; exactly one is allowed", i)
		case hasJudge:
			if ops > 0 {
				return fmt.Errorf("done_when.all[%d] is a judge leaf and must not set comparison operators (eq/ne/in/gte/lte)", i)
			}
			for _, r := range leaf.Relation {
				if !domain.AssignableJudgeRelation(domain.SessionRelation(r)) {
					return fmt.Errorf("done_when.all[%d] judge relation %q is not assignable; use sibling/parent/child (self is always rejected)", i, r)
				}
			}
		case hasCheck:
			if ops != 1 {
				return fmt.Errorf("done_when.all[%d] check %q must set exactly one operator (eq/ne/in/gte/lte), got %d", i, leaf.Check, ops)
			}
			if len(leaf.Relation) > 0 {
				return fmt.Errorf("done_when.all[%d] check leaf must not set judge relation policy", i)
			}
		default:
			if ops > 0 {
				return fmt.Errorf("done_when.all[%d] sets a comparison operator without a `check` output name", i)
			}
			return fmt.Errorf("done_when.all[%d] sets neither `check` nor `judge`; exactly one is required", i)
		}
	}
	return nil
}

// DynamicOutput sources outputs from a script's stdout: Name takes the whole
// stdout; Produces takes a JSON object keyed by those names, so one fetch feeds
// several outputs instead of re-running the API per field. Internal (default
// true) keeps a value out of other tasks' inputs.
type DynamicOutput struct {
	Name     string   `toml:"name"`
	Produces []string `toml:"produces"`
	Script   string   `toml:"script"`
	Internal *bool    `toml:"internal"`
	// FromResourceStatus sources the produced keys from the instance's bound
	// resource (--resource) instead of a script: it looks up the resource
	// definition (resources/*.toml) matching .ResourceID, runs its `observe`,
	// and copies the named keys from the result. The declarative alternative
	// to a task writing its own `sennit resource status "{{.ResourceID}}"`
	// wrapper (ADR "goal-as-task" D1/D2 resolution face). Mutually exclusive
	// with Script.
	FromResourceStatus bool `toml:"from_resource_status"`
}

// IsInternal defaults to true.
func (o DynamicOutput) IsInternal() bool {
	return o.Internal == nil || *o.Internal
}

func (o DynamicOutput) OutputNames() []string {
	if o.Name != "" {
		return []string{o.Name}
	}
	return o.Produces
}

// ValidateDynamicOutputs: exactly one of script/from_resource_status and
// exactly one of name/produces per entry, non-empty names, unique across the
// task. from_resource_status requires `produces` — a resource observation is
// always a JSON object of named fields, so there is no single-string `name`
// form to fill from it.
func ValidateDynamicOutputs(srcs []DynamicOutput) error {
	seen := make(map[string]bool, len(srcs))
	for i, o := range srcs {
		if (strings.TrimSpace(o.Script) != "") == o.FromResourceStatus {
			return fmt.Errorf("output[%d] must set exactly one of `script` or `from_resource_status`", i)
		}
		if o.FromResourceStatus && o.Name != "" {
			return fmt.Errorf("output[%d] sets `from_resource_status` with `name`; it requires `produces`", i)
		}
		if (strings.TrimSpace(o.Name) != "") == (len(o.Produces) > 0) {
			return fmt.Errorf("output[%d] must set exactly one of `name` or `produces`", i)
		}
		for _, n := range o.OutputNames() {
			if strings.TrimSpace(n) == "" {
				return fmt.Errorf("output[%d] has an empty output name", i)
			}
			if seen[n] {
				return fmt.Errorf("output %q is declared more than once", n)
			}
			seen[n] = true
		}
	}
	return nil
}

// builtinDynamicOutputs let a local DoD ride the same script→value→check path
// as a remote one with no per-task boilerplate.
var builtinDynamicOutputs = []DynamicOutput{{
	Name:   "worktree_dirty",
	Script: "git -C {{.WorktreePath}} status --porcelain | wc -l | tr -d ' '",
}}

// withBuiltinOutputs injects a builtin only into tasks whose done_when names
// it, so one that never reads it never shells out for it; a same-named
// user-declared output wins.
func withBuiltinOutputs(def TaskDefinition) TaskDefinition {
	if def.DoneWhen == nil {
		return def
	}
	declared := make(map[string]bool, len(def.DynamicOutputs))
	for _, o := range def.DynamicOutputs {
		for _, n := range o.OutputNames() {
			declared[n] = true
		}
	}
	var inject []DynamicOutput
	for _, b := range builtinDynamicOutputs {
		referenced := false
		for _, leaf := range def.DoneWhen.All {
			if strings.TrimSpace(leaf.Check) == b.Name {
				referenced = true
				break
			}
		}
		if referenced && !declared[b.Name] {
			inject = append(inject, b)
		}
	}
	if len(inject) == 0 {
		return def
	}
	merged := make([]DynamicOutput, 0, len(def.DynamicOutputs)+len(inject))
	merged = append(merged, def.DynamicOutputs...)
	merged = append(merged, inject...)
	def.DynamicOutputs = merged
	return def
}

// EffectiveScope returns the scope, defaulting to "run" when unset.
func (d TaskDefinition) EffectiveScope() string {
	if d.Scope == "" {
		return TaskScopeRun
	}
	return d.Scope
}

// ResolvedOutputsSchemaPath joins OutputsSchemaFile with BaseDir.
func (d TaskDefinition) ResolvedOutputsSchemaPath() string {
	return resolveSchemaPath(d.OutputsSchemaFile, d.BaseDir)
}

// ResolvedInputsSchemaPath joins InputsSchemaFile with BaseDir.
func (d TaskDefinition) ResolvedInputsSchemaPath() string {
	return resolveSchemaPath(d.InputsSchemaFile, d.BaseDir)
}

// ResolvedInputsSchemaPath joins the workflow file's InputsSchemaFile with BaseDir.
func (w WorkflowFile) ResolvedInputsSchemaPath() string {
	return resolveSchemaPath(w.InputsSchemaFile, w.BaseDir)
}

func resolveSchemaPath(file, baseDir string) string {
	if file == "" {
		return ""
	}
	if filepath.IsAbs(file) {
		return file
	}
	if baseDir == "" {
		return file
	}
	return filepath.Join(baseDir, file)
}

// layerDir pairs a cascade search directory with its trust classification.
// The workdir layer (`.sennit/` inside the working directory itself) is clone
// content — an attacker-controlled repository must not be able to introduce
// shell that sennit would execute. Every other layer (plugin, global, ancestor
// overlays above the worktree) is machine-owned and trusted.
type layerDir struct {
	dir     string
	workdir bool
}

// LoadWorkflows merges plugin + global + ancestor `.sennit/workflows/` layers so
// projects can extend (not fork) shared workflows. Same-stem files append
// nodes; duplicating a node id across layers is rejected so a deeper layer
// can't silently stomp a base node.
//
// Trust restriction: workflow files in the workdir layer may only add nodes.
// Declaring anything else there (setup/cleanup shell, identity, schemas) is a
// load error. Declaring `done_when` at the workflow level, in any layer, is
// also a load error — the completion predicate lives on the task definition's
// `[done_when]`.
func (c *Config) LoadWorkflows(worktreeDir string) (map[string]WorkflowFile, error) {
	dirs := c.workflowSearchDirs(worktreeDir)
	layered := make(map[string][]WorkflowFile)
	order := make([]string, 0)
	for _, layer := range dirs {
		entries, err := listTOMLFiles(layer.dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			wf, err := loadWorkflowFile(path)
			if err != nil {
				return nil, fmt.Errorf("workflow %s: %w", path, err)
			}
			if layer.workdir {
				if err := validateWorkdirLayerWorkflow(wf); err != nil {
					return nil, err
				}
			}
			if _, seen := layered[wf.ID]; !seen {
				order = append(order, wf.ID)
			}
			layered[wf.ID] = append(layered[wf.ID], wf)
		}
	}
	out := make(map[string]WorkflowFile, len(layered))
	for _, id := range order {
		merged, err := mergeWorkflowLayers(layered[id])
		if err != nil {
			return nil, err
		}
		out[id] = merged
	}
	return out, nil
}

// validateWorkdirLayerWorkflow enforces the node-addition-only rule for
// workflow files that live inside the working directory.
func validateWorkdirLayerWorkflow(wf WorkflowFile) error {
	var offending []string
	if wf.Name != "" {
		offending = append(offending, "name")
	}
	if wf.Description != "" {
		offending = append(offending, "description")
	}
	if wf.Provider != "" {
		offending = append(offending, "provider")
	}
	if wf.Environment != "" {
		offending = append(offending, "environment")
	}
	if len(wf.EnvironmentInputs) > 0 {
		offending = append(offending, "environment_inputs")
	}
	if len(wf.Display) > 0 {
		offending = append(offending, "display")
	}
	if wf.AutoSelect != nil {
		offending = append(offending, "auto_select")
	}
	if len(wf.InputsSchema) > 0 || wf.InputsSchemaFile != "" {
		offending = append(offending, "inputs_schema")
	}
	// A channel selects a delivery primitive (exec runs argv) — clone content
	// must not introduce one, same trust rule as tasks/providers.
	if len(wf.Event.Channel) > 0 {
		offending = append(offending, "event.channel")
	}
	// [tick] is a declaration that produces automatic execution (the reactor
	// ticks on it with no human/orchestrator in the loop); clone content must
	// not be able to supply a driving condition any more than it can supply
	// setup/cleanup shell or an event channel's exec target.
	if wf.Tick != nil {
		offending = append(offending, "tick")
	}
	if len(offending) > 0 {
		return fmt.Errorf("workflow %s: a `.sennit/workflows/` file inside the working directory may only add [[nodes]]; %v must move to a trusted layer (global config, plugin, or a directory above the worktree)", wf.SourcePath, offending)
	}
	return nil
}

// LoadTaskDefinitions merges plugin + global + ancestor `.sennit/tasks/`
// layers. Same-id deeper layer wins because setup/cleanup is atomic —
// appending two shell scripts doesn't have a sensible meaning the way
// appending nodes does.
//
// Trust restriction: task definitions are arbitrary shell, so the workdir
// layer (clone content) must not contribute any. A `.sennit/tasks/*.toml`
// inside the working directory is a load error rather than a silent skip —
// silently ignoring it would make the author think the task is active.
func (c *Config) LoadTaskDefinitions(worktreeDir string) (map[string]TaskDefinition, error) {
	out := make(map[string]TaskDefinition)
	dirs := c.tasksSearchDirs(worktreeDir)
	for _, layer := range dirs {
		entries, err := listTOMLFiles(layer.dir)
		if err != nil {
			return nil, err
		}
		if layer.workdir && len(entries) > 0 {
			return nil, fmt.Errorf("task definitions inside the working directory are not loaded (clone content must not carry shell): %s; move them to the global layer (~/.config/sennit/tasks/), a plugin, or a repo overlay above the worktree", entries[0])
		}
		for _, path := range entries {
			def, err := loadTaskDefinitionFile(path)
			if err != nil {
				return nil, fmt.Errorf("task %s: %w", path, err)
			}
			out[def.ID] = withBuiltinOutputs(def)
		}
	}
	return out, nil
}

// workflowSearchDirs orders the cascade: plugins (base) → global → ancestors
// (outermost-first, ending at the worktree itself — the only untrusted layer).
func (c *Config) workflowSearchDirs(worktreeDir string) []layerDir {
	return c.searchDirs(worktreeDir, "workflows")
}

func (c *Config) tasksSearchDirs(worktreeDir string) []layerDir {
	return c.searchDirs(worktreeDir, "tasks")
}

func (c *Config) searchDirs(worktreeDir, kind string) []layerDir {
	var dirs []layerDir
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, layerDir{dir: filepath.Join(plugin, kind)})
	}
	if c.BaseDir != "" {
		dirs = append(dirs, layerDir{dir: filepath.Join(c.BaseDir, kind)})
	}
	cleanWorktree := ""
	if worktreeDir != "" {
		cleanWorktree = filepath.Clean(worktreeDir)
	}
	for _, anc := range cascadeAncestors(worktreeDir) {
		dirs = append(dirs, layerDir{
			dir:     filepath.Join(anc, ".sennit", kind),
			workdir: anc == cleanWorktree,
		})
	}
	return dirs
}

// cascadeAncestors walks up from worktreeDir, ordered outermost-first.
// $HOME is the exclusive upper bound because the user's global config lives
// at `~/.config/sennit/` and `$HOME/.sennit/` would collide with that.
func cascadeAncestors(worktreeDir string) []string {
	if worktreeDir == "" {
		return nil
	}
	var cleanHome string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome = filepath.Clean(home)
	}
	var chain []string
	cur := filepath.Clean(worktreeDir)
	for {
		if cur == cleanHome || cur == string(filepath.Separator) {
			break
		}
		chain = append(chain, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func mergeWorkflowLayers(layers []WorkflowFile) (WorkflowFile, error) {
	if len(layers) == 0 {
		return WorkflowFile{}, fmt.Errorf("internal: mergeWorkflowLayers called with no layers")
	}
	if len(layers) == 1 {
		return layers[0], nil
	}
	merged := layers[0]
	merged.Nodes = append([]WorkflowNode(nil), layers[0].Nodes...)
	merged.Event.Channel = append([]EventChannel(nil), layers[0].Event.Channel...)
	nameSource := ""
	descSource := ""
	providerSource := ""
	environmentSource := ""
	environmentInputsSource := ""
	autoSelectSource := ""
	if merged.Name != "" {
		nameSource = layers[0].SourcePath
	}
	if merged.Description != "" {
		descSource = layers[0].SourcePath
	}
	if merged.Provider != "" {
		providerSource = layers[0].SourcePath
	}
	if merged.Environment != "" {
		environmentSource = layers[0].SourcePath
	}
	if len(merged.EnvironmentInputs) > 0 {
		environmentInputsSource = layers[0].SourcePath
	}
	if merged.AutoSelect != nil {
		autoSelectSource = layers[0].SourcePath
	}
	displaySource := ""
	if len(merged.Display) > 0 {
		displaySource = layers[0].SourcePath
	}
	nodeSource := make(map[string]string, len(merged.Nodes))
	for _, n := range merged.Nodes {
		if n.ID != "" {
			nodeSource[n.ID] = layers[0].SourcePath
		}
	}
	channelSource := make(map[string]string, len(merged.Event.Channel))
	for _, ch := range merged.Event.Channel {
		if ch.Name != "" {
			channelSource[ch.Name] = layers[0].SourcePath
		}
	}
	for _, layer := range layers[1:] {
		if layer.Name != "" {
			if nameSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `name` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, nameSource, layer.SourcePath)
			}
			merged.Name = layer.Name
			nameSource = layer.SourcePath
		}
		if layer.Description != "" {
			if descSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `description` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, descSource, layer.SourcePath)
			}
			merged.Description = layer.Description
			descSource = layer.SourcePath
		}
		if layer.Provider != "" {
			if providerSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `provider` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, providerSource, layer.SourcePath)
			}
			merged.Provider = layer.Provider
			providerSource = layer.SourcePath
		}
		if layer.Environment != "" {
			if environmentSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `environment` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, environmentSource, layer.SourcePath)
			}
			merged.Environment = layer.Environment
			environmentSource = layer.SourcePath
		}
		if len(layer.EnvironmentInputs) > 0 {
			if environmentInputsSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `environment_inputs` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, environmentInputsSource, layer.SourcePath)
			}
			merged.EnvironmentInputs = layer.EnvironmentInputs
			environmentInputsSource = layer.SourcePath
		}
		if layer.AutoSelect != nil {
			if autoSelectSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `auto_select` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, autoSelectSource, layer.SourcePath)
			}
			merged.AutoSelect = layer.AutoSelect
			autoSelectSource = layer.SourcePath
		}
		if len(layer.Display) > 0 {
			if displaySource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `display` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, displaySource, layer.SourcePath)
			}
			merged.Display = layer.Display
			displaySource = layer.SourcePath
		}
		// [tick] is deeper-wins whole-table replacement (like TaskDefinition),
		// not additive/no-redeclare like the fields above: it is a runtime
		// tuning knob (CI length, API quota), not an identity field, so a
		// deeper layer's local judgment should be free to override it outright
		// (docs/wiki/workflow-provider-config.md). A global default declared
		// after a deeper layer already has `[tick]` must not become a load
		// error, so redeclaration is silently allowed here.
		if layer.Tick != nil {
			merged.Tick = layer.Tick
		}
		for _, n := range layer.Nodes {
			if n.ID != "" {
				if prev, dup := nodeSource[n.ID]; dup {
					return WorkflowFile{}, fmt.Errorf("workflow %q: node id %q is declared in both %s and %s; cascade layers may add new nodes but cannot redeclare existing ones", merged.ID, n.ID, prev, layer.SourcePath)
				}
				nodeSource[n.ID] = layer.SourcePath
			}
			merged.Nodes = append(merged.Nodes, n)
		}
		for _, ch := range layer.Event.Channel {
			if ch.Name != "" {
				if prev, dup := channelSource[ch.Name]; dup {
					return WorkflowFile{}, fmt.Errorf("workflow %q: event.channel name %q is declared in both %s and %s; cascade layers may add new channels but cannot redeclare existing ones", merged.ID, ch.Name, prev, layer.SourcePath)
				}
				channelSource[ch.Name] = layer.SourcePath
			}
			merged.Event.Channel = append(merged.Event.Channel, ch)
		}
	}
	schema, err := combineInputsSchemas(layers)
	if err != nil {
		return WorkflowFile{}, fmt.Errorf("workflow %q: %w", merged.ID, err)
	}
	merged.InputsSchema = schema
	merged.InputsSchemaFile = ""
	return merged, nil
}

// combineInputsSchemas wraps multiple layers in allOf so each layer keeps
// enforcing its own contract instead of one silently overriding another.
func combineInputsSchemas(layers []WorkflowFile) (map[string]any, error) {
	var parts []map[string]any
	for _, layer := range layers {
		hasInline := len(layer.InputsSchema) > 0
		hasFile := layer.InputsSchemaFile != ""
		if hasInline && hasFile {
			return nil, fmt.Errorf("%s: inline inputs_schema and inputs_schema_file are mutually exclusive", layer.SourcePath)
		}
		switch {
		case hasInline:
			parts = append(parts, layer.InputsSchema)
		case hasFile:
			path := layer.ResolvedInputsSchemaPath()
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			parts = append(parts, m)
		}
	}
	switch len(parts) {
	case 0:
		return nil, nil
	case 1:
		return parts[0], nil
	default:
		anyParts := make([]any, len(parts))
		for i, p := range parts {
			anyParts[i] = p
		}
		return map[string]any{"allOf": anyParts}, nil
	}
}

// listTOMLFiles returns sorted *.toml entries in dir. A missing dir returns an
// empty list (not an error) so missing repo/global directories are normal.
func listTOMLFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// workflowStemRE constrains workflow filenames: first character must be
// alphanumeric (so `-flag-like` doesn't look like a CLI flag, and hidden
// `.toml` dotfiles are rejected), the rest may add `_` / `-` / `.`. Workflow
// ids only ever appear as plain strings (CLI flag, URN segment, state.json
// key) so they don't need to satisfy `nodeIDRE`'s Go-template constraint.
var workflowStemRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// taskStemRE constrains task filenames. Task ids feed into both `uses =`
// (plain string) and — when `uses` is omitted — the node id, which *is* parsed
// by text/template as a dotted field access. Forcing task filenames to the
// underscore form means `[[nodes]] id = "claude"` can omit `uses` for every
// dogfood task, regardless of whether the workflow author remembered the
// hyphen-vs-underscore split.
var taskStemRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func loadWorkflowFile(path string) (WorkflowFile, error) {
	stem, err := validateStem(path, workflowStemRE, "workflow")
	if err != nil {
		return WorkflowFile{}, err
	}
	var wf WorkflowFile
	md, err := toml.DecodeFile(path, &wf)
	if err != nil {
		return wf, err
	}
	for _, key := range md.Undecoded() {
		if len(key) == 1 && key[0] == "done_when" {
			return wf, fmt.Errorf("workflow %s: `done_when` is retired at the workflow level; declare the completion predicate on the task definition's `[done_when]` instead", path)
		}
		if len(key) == 2 && key[0] == "tick" && key[1] == "stale_when" {
			return wf, fmt.Errorf("workflow %s: `tick.stale_when` was renamed to `tick.heartbeat`; update the workflow file", path)
		}
		if len(key) == 2 && key[0] == "tick" && key[1] == "max_stale_when" {
			return wf, fmt.Errorf("workflow %s: `tick.max_stale_when` was renamed to `tick.max_heartbeat`; update the workflow file", path)
		}
	}
	wf.ID = stem
	wf.SourcePath = path
	wf.BaseDir = configFileDir(path)
	// Cross-layer name dups are caught at merge, but a single-layer workflow
	// short-circuits merge, so catch within-file dups here.
	seen := make(map[string]bool, len(wf.Event.Channel))
	for _, ch := range wf.Event.Channel {
		if ch.Name == "" {
			continue
		}
		if seen[ch.Name] {
			return wf, fmt.Errorf("event.channel name %q is declared more than once", ch.Name)
		}
		seen[ch.Name] = true
	}
	return wf, nil
}

func loadTaskDefinitionFile(path string) (TaskDefinition, error) {
	stem, err := validateStem(path, taskStemRE, "task")
	if err != nil {
		return TaskDefinition{}, err
	}
	var def TaskDefinition
	if _, err := toml.DecodeFile(path, &def); err != nil {
		return def, err
	}
	if err := def.DoneWhen.Validate(); err != nil {
		return def, err
	}
	def.ID = stem
	def.SourcePath = path
	def.BaseDir = configFileDir(path)
	if err := validateTaskChains(&def); err != nil {
		return def, err
	}
	return def, nil
}

func validateStem(path string, re *regexp.Regexp, kind string) (string, error) {
	base := filepath.Base(path)
	stem := base[:len(base)-len(filepath.Ext(base))]
	if !re.MatchString(stem) {
		return "", fmt.Errorf("%s filename stem %q is not allowed (must match %s)", kind, stem, re.String())
	}
	return stem, nil
}
