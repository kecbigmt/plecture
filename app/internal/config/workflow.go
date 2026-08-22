package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// WorkflowFile is loaded from `.plect/workflows/<id>.toml` (per-repo) or from
// `~/.config/plect/workflows/<id>.toml` (global). Per-repo wins on id conflict.
//
// A workflow is a named bundle of nodes. Each node selects a task definition
// via `uses` and binds its inputs as Go templates. The setup/cleanup DAG is
// derived from `.Nodes.<id>.outputs.<key>` references in those bindings, so
// `depends_on` is no longer part of the node surface.
//
// ID is derived from the filename stem and is *not* read from TOML — the
// filename is the single source of truth (renaming a workflow is `mv`, not
// `mv` + TOML edit). `name` is the human-readable display label (separate from
// identity); `description` is a short summary used by `plect workflow list/show`
// and the MCP discovery tools.
type WorkflowFile struct {
	ID          string `toml:"-"`
	Name        string `toml:"name"`
	Description string `toml:"description"`
	// WorkspaceProvider names the workspace provider (workspaces/<id>.toml in
	// the trusted base layers) this workflow runs on. The workspace provider
	// owns the resource-kind knowledge — resolver, workspace setup/cleanup,
	// the @workflow outputs contract; the workflow owns the task shape on top
	// (nodes, inputs, done_when, display). A workflow without one cannot
	// acquire a workspace, so it cannot back a session.
	WorkspaceProvider string `toml:"workspace_provider"`
	// WorkspaceProviderInputs sets the workspace provider's author-declared
	// parameters (its `[inputs_schema]`). Values are literal data, not
	// templates — the provider's hooks run before any workspace exists, so
	// there is no node output for a parameter to reference.
	WorkspaceProviderInputs map[string]string `toml:"workspace_provider_inputs"`
	// Display declares the values shown by `plect ls` / `show` / the web UI as
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
	// session advances only via manual `plect tick` and the judge builtin.
	Tick *TickConfig `toml:"tick"`
	// Healthcheck declares the dedicated health sampling clock for sessions
	// produced by this workflow. Like `[tick]`, `[healthcheck]` is a
	// deeper-wins whole-table runtime tuning declaration. It names the
	// sampling cycle, not what health means: what each probe observes is a
	// task-level `[health]` declaration (see HealthConfig).
	Healthcheck      *HealthcheckConfig `toml:"healthcheck"`
	InputsSchema     map[string]any     `toml:"inputs_schema"`
	InputsSchemaFile string             `toml:"inputs_schema_file"`
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
// means manual `plect tick` and the judge builtin are the only drivers.
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
}

// DefaultMaxHeartbeat caps the quiet-tick backoff interval when a workflow's
// `[tick]` declares no `max_heartbeat`: standing goals must keep observing
// at some ceiling even after many quiet sweeps, so backoff never fully
// suppresses ticking.
const DefaultMaxHeartbeat = 8 * time.Hour

// MaxHeartbeatOrDefault returns t's declared `max_heartbeat` cap, falling
// back to DefaultMaxHeartbeat when the workflow declares none.
func (t TickConfig) MaxHeartbeatOrDefault() time.Duration {
	if t.MaxHeartbeat.Duration > 0 {
		return t.MaxHeartbeat.Duration
	}
	return DefaultMaxHeartbeat
}

// BackoffInterval computes heartbeat * 2^n capped at max. Doubling
// iteratively (rather than shifting n) keeps this well-defined for large n
// without overflowing time.Duration. Shared by the tick reactor's own
// quiet-tick backoff (internal/reactor) and the heartbeat deadman check
// (internal/service): both must judge a session against the same effective
// interval the reactor is actually honoring right now, not the bare
// declared `heartbeat` value, or a session legitimately backed off from
// quiet reads as a false stall.
func BackoffInterval(base, max time.Duration, n int) time.Duration {
	interval := base
	for i := 0; i < n && interval < max; i++ {
		interval *= 2
	}
	if interval > max {
		interval = max
	}
	return interval
}

// HealthcheckConfig declares the dedicated healthcheck cycle for a workflow.
// Unlike `[tick].heartbeat`, this clock has no quiet backoff: it is the
// health side's stall accelerator, so it keeps sampling even when the
// done_when brake has backed off.
type HealthcheckConfig struct {
	Period         Duration `toml:"period"`
	StallThreshold Duration `toml:"stall_threshold"`
	RenotifyEvery  int      `toml:"renotify_every"`
}

// DefaultHealthcheckConfig returns the workflow healthcheck defaults.
func DefaultHealthcheckConfig() HealthcheckConfig {
	period := 5 * time.Minute
	return HealthcheckConfig{
		Period:         Duration{Duration: period},
		StallThreshold: Duration{Duration: 3 * period},
		RenotifyEvery:  3,
	}
}

// NormalizeHealthcheckConfig applies defaults to an optional workflow
// healthcheck declaration.
func NormalizeHealthcheckConfig(in *HealthcheckConfig) HealthcheckConfig {
	out := DefaultHealthcheckConfig()
	if in == nil {
		return out
	}
	if in.Period.Duration > 0 {
		out.Period = in.Period
	}
	if in.StallThreshold.Duration > 0 {
		out.StallThreshold = in.StallThreshold
	} else {
		out.StallThreshold = Duration{Duration: 3 * out.Period.Duration}
	}
	if in.RenotifyEvery > 0 {
		out.RenotifyEvery = in.RenotifyEvery
	}
	return out
}

// WorkflowNode is a single instantiation of a task definition within a
// workflow. ID is the state key (independent of TaskID = `uses`) and Inputs
// is the template-string mapping fed to the task's setup/cleanup as `.Input.*`.
//
// Blocks declares reverse dependency edges: each listed node id becomes a
// dependent of this one, equivalent to writing the inverse dependency in the
// listed node. Use it when a cascade overlay needs to insert itself ahead of
// base nodes it cannot modify directly (e.g. a workspace-dir-process killer
// that must clean up after the runtime/agent tasks).
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
//	include = ["plect.instruction", "resource.*", "user.emit"]
type EventChannel struct {
	Name    string            `toml:"name"`
	Uses    string            `toml:"uses"`
	Inputs  map[string]string `toml:"inputs"`
	Include []string          `toml:"include"`
}

// TaskDefinition is one `kind = "effect"` declaration, loaded from a
// definition document under a `tasks/` directory in the trusted base layers.
// Effects are reusable — several workflows may reference the same declaration
// via `uses`, and one workflow may instantiate it under different node ids.
//
// The id is the definition table's name. Effects intentionally have no
// `depends_on`: wiring is the workflow's job.
type TaskDefinition struct {
	ID      string
	Scope   string
	Setup   *lang.Action
	Cleanup *lang.Action
	// Inner names the next effect inward, making this declaration the outer
	// layer of a nesting chain: either a plain id (resolved in the merged
	// namespace, or in the declaring plugin's own namespace when the
	// declaration is plugin-authored) or a catalog-qualified reference.
	Inner string
	// InnerInputs and InnerEnv are the joint inward: the input object passed
	// to the inner effect, and the environment added to its executions.
	InnerInputs map[string]*lang.Value
	InnerEnv    map[string]*lang.Value
	// OutputsBind is the joint outward: this layer's public outputs, each
	// projected or computed from an inner output, a local, or an input.
	OutputsBind map[string]*lang.Value
	// LocalsSchema / LocalsSchemaFile is the contract for the private
	// intermediates this layer's setup emits.
	LocalsSchema     map[string]any
	LocalsSchemaFile string
	// InnerChain is the nesting chain inward from this effect, next-inner
	// first and innermost last, stamped by load-time resolution. Empty for a
	// plain effect.
	InnerChain []TaskDefinition
	// Health declares the effect's `[health]` table — the alive and activity
	// probes that determine its contribution to session health. Nil for an
	// effect that declares neither.
	Health *HealthConfig
	// Terminal declares the effect's `[terminal]` table: the effect owns an
	// interactive endpoint and offers attach/capture/send_text/send_keys
	// against it.
	Terminal          *TerminalConfig
	OutputsSchema     map[string]any
	OutputsSchemaFile string
	InputsSchema      map[string]any
	InputsSchemaFile  string
	BaseDir           string
	SourcePath        string
	// FromPlugin says a plugin layer wrote this declaration, which is what
	// decides whether its bin references may name another plugin.
	FromPlugin bool
}

// Ownership names the layer that wrote this declaration, for the reference
// rules that differ between shipped and user-authored config.
func (d TaskDefinition) Ownership() lang.Ownership {
	return lang.Ownership{IsPlugin: d.FromPlugin}
}

// DoneWhen is the structured completion predicate a task definition may
// declare. It is a conjunction of leaves (All) plus an optional Budget.
//
// done_when has two leaf kinds: check leaves compare observed outputs, while
// judge leaves wait for independent reviewer input recorded by `plect judge`.
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
	// Check names one completion key by its root path — `resource.state.<key>`
	// for what the declared observer publishes, `self.state.<key>` for what
	// this task holds.
	Check string   `toml:"check" json:"check,omitempty"`
	Eq    *string  `toml:"eq" json:"eq,omitempty"`
	Ne    *string  `toml:"ne" json:"ne,omitempty"`
	In    []any    `toml:"in" json:"in,omitempty"`
	Gte   *float64 `toml:"gte" json:"gte,omitempty"`
	Lte   *float64 `toml:"lte" json:"lte,omitempty"`
	// Expr states a predicate computed over those same roots, which is how a
	// recorded value is compared against a live one — a comparison with no key
	// of its own to hang on.
	Expr     string   `toml:"expr" json:"expr,omitempty"`
	Judge    string   `toml:"judge" json:"judge,omitempty"`
	ID       string   `toml:"id" json:"id,omitempty"`
	Relation []string `toml:"relation" json:"relation,omitempty"`
}

// IsCheck / IsJudge / IsExpr name which of the three leaf kinds this is.
// Exactly one holds for a valid leaf.
func (l DoneWhenLeaf) IsCheck() bool { return strings.TrimSpace(l.Check) != "" }
func (l DoneWhenLeaf) IsJudge() bool { return strings.TrimSpace(l.Judge) != "" }
func (l DoneWhenLeaf) IsExpr() bool  { return strings.TrimSpace(l.Expr) != "" }

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

// Validate checks the structural invariants of a done_when: it declares at
// least one leaf, and each leaf is exactly one of the three kinds — a check
// naming one key and setting one operator, an expression, or a judge.
// Operator value typing (e.g. gte must be numeric) is enforced at TOML decode
// by the field types; which roots a key or an expression may read is the
// language's own check.
func (d *DoneWhen) Validate() error {
	if d == nil {
		return nil
	}
	if len(d.All) == 0 {
		return fmt.Errorf("done_when declares no leaves; add at least one `all` entry or remove the table")
	}
	for i, leaf := range d.All {
		kinds := 0
		for _, is := range []bool{leaf.IsCheck(), leaf.IsExpr(), leaf.IsJudge()} {
			if is {
				kinds++
			}
		}
		ops := leaf.operatorCount()
		switch {
		case kinds > 1:
			return fmt.Errorf("done_when.all[%d] sets more than one of `check`, `expr`, and `judge`; exactly one is allowed", i)
		case leaf.IsJudge():
			if ops > 0 {
				return fmt.Errorf("done_when.all[%d] is a judge leaf and must not set comparison operators (eq/ne/in/gte/lte)", i)
			}
			for _, r := range leaf.Relation {
				if !domain.AssignableJudgeRelation(domain.SessionRelation(r)) {
					return fmt.Errorf("done_when.all[%d] judge relation %q is not assignable; use sibling/parent/child (self is always rejected)", i, r)
				}
			}
		case leaf.IsExpr():
			if ops > 0 {
				return fmt.Errorf("done_when.all[%d] is an expression leaf and must not set comparison operators (eq/ne/in/gte/lte); the expression states the whole predicate", i)
			}
			if len(leaf.Relation) > 0 {
				return fmt.Errorf("done_when.all[%d] expression leaf must not set judge relation policy", i)
			}
		case leaf.IsCheck():
			if ops != 1 {
				return fmt.Errorf("done_when.all[%d] check %q must set exactly one operator (eq/ne/in/gte/lte), got %d", i, leaf.Check, ops)
			}
			if len(leaf.Relation) > 0 {
				return fmt.Errorf("done_when.all[%d] check leaf must not set judge relation policy", i)
			}
		default:
			if ops > 0 {
				return fmt.Errorf("done_when.all[%d] sets a comparison operator without a `check` key", i)
			}
			return fmt.Errorf("done_when.all[%d] sets none of `check`, `expr`, and `judge`; exactly one is required", i)
		}
	}
	return nil
}

// EffectiveScope returns the scope, defaulting to "run" when unset. A nested
// task that declares none takes the innermost task's scope rather than the
// plain-task default: the chain runs as one task, so the layer that owns the
// actual resource decides the lifecycle it belongs to.
func (d TaskDefinition) EffectiveScope() string {
	if d.Scope != "" {
		return d.Scope
	}
	if n := len(d.InnerChain); n > 0 {
		return d.InnerChain[n-1].EffectiveScope()
	}
	return TaskScopeRun
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
// The workspace-dir layer (`.plect/` inside the workspace directory itself)
// is clone content — an attacker-controlled repository must not be able to
// introduce shell that plect would execute. Every other layer (plugin,
// global, ancestor overlays above the workspace dir) is machine-owned and
// trusted.
//
// plugin marks a layer sourced from c.PluginDirs specifically (as opposed to
// the global config dir or an ancestor overlay): per the plugin-packaging
// design, a same-id conflict between two plugin layers is a load error,
// while a user-owned layer (global or overlay) may always replace what a
// plugin layer defines.
type layerDir struct {
	dir          string
	workspaceDir bool
	plugin       bool
	// pluginID is the catalog-qualified identity of the plugin this layer
	// was mounted from, empty for a hand-authored plugin_dirs entry (which
	// carries no catalog identity) and for the non-plugin layers.
	pluginID string
}

// LoadWorkflows merges plugin + global + ancestor `.plect/workflows/` layers so
// projects can extend (not fork) shared workflows. Same-stem files append
// nodes; duplicating a node id across layers is rejected so a deeper layer
// can't silently stomp a base node. Same-id across two different plugin
// layers is rejected outright, before any merge is attempted: composing two
// arbitrary plugins under one workflow id was never a sanctioned use case —
// only a user-owned overlay may extend a plugin's workflow.
//
// Trust restriction: workflow files in the workspace-dir layer may only add
// nodes. Declaring anything else there (setup/cleanup shell, identity,
// schemas) is a load error. Declaring `done_when` at the workflow level, in
// any layer, is also a load error — the completion predicate lives on the
// task definition's `[done_when]`.
func (c *Config) LoadWorkflows(workspaceDirPath string) (map[string]WorkflowFile, error) {
	dirs := c.workflowSearchDirs(workspaceDirPath)
	layered := make(map[string][]WorkflowFile)
	order := make([]string, 0)
	pluginOwner := make(map[string]string)
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
			if layer.workspaceDir {
				if err := validateWorkspaceDirLayerWorkflow(wf); err != nil {
					return nil, err
				}
			}
			if layer.plugin {
				if owner, exists := pluginOwner[wf.ID]; exists {
					return nil, fmt.Errorf("workflow %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", wf.ID, owner, path)
				}
				pluginOwner[wf.ID] = path
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

// validateWorkspaceDirLayerWorkflow enforces the node-addition-only rule for
// workflow files that live inside the workspace directory.
func validateWorkspaceDirLayerWorkflow(wf WorkflowFile) error {
	var offending []string
	if wf.Name != "" {
		offending = append(offending, "name")
	}
	if wf.Description != "" {
		offending = append(offending, "description")
	}
	if wf.WorkspaceProvider != "" {
		offending = append(offending, "workspace_provider")
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
	// must not introduce one, same trust rule as tasks/workspaces.
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
	if wf.Healthcheck != nil {
		offending = append(offending, "healthcheck")
	}
	if len(offending) > 0 {
		return fmt.Errorf("workflow %s: a `.plect/workflows/` file inside the workspace directory may only add [[nodes]]; %v must move to a trusted layer (global config, plugin, or a directory above the workspace dir)", wf.SourcePath, offending)
	}
	return nil
}

// LoadTaskDefinitions merges plugin + global + ancestor `.plect/tasks/`
// layers. Same-id deeper (user-owned) layer wins because setup/cleanup is
// atomic — appending two shell scripts doesn't have a sensible meaning the
// way appending nodes does. Same-id across two different plugin layers is a
// load error instead of a silent pick, since declaration order must never
// decide between two plugins' shell.
//
// Trust restriction: task definitions are arbitrary shell, so the
// workspace-dir layer (clone content) must not contribute any. A
// `.plect/tasks/*.toml` inside the workspace directory is a load error
// rather than a silent skip — silently ignoring it would make the author
// think the task is active.
func (c *Config) LoadTaskDefinitions(workspaceDirPath string) (map[string]TaskDefinition, error) {
	out := make(map[string]TaskDefinition)
	pluginOwner := make(map[string]string)
	// all keeps every definition file that was read, including a plugin
	// definition a same-id user task shadows in out: a catalog-qualified
	// `inner` reference exists precisely to reach one.
	var all []TaskDefinition
	pluginOfSource := make(map[string]string)
	dirs := c.tasksSearchDirs(workspaceDirPath)
	for _, layer := range dirs {
		entries, err := listTOMLFiles(layer.dir)
		if err != nil {
			return nil, err
		}
		if layer.workspaceDir && len(entries) > 0 {
			return nil, fmt.Errorf("task definitions inside the workspace directory are not loaded (clone content must not carry shell): %s; move them to the global layer (~/.config/plect/tasks/), a plugin, or a repo overlay above the workspace dir", entries[0])
		}
		// One layer spreads its declarations over as many files as it likes,
		// but an id is unique within it: resolving a same-layer collision by
		// traversal order would let a filename decide which of two
		// declarations is live. A deeper layer replacing a shallower same-id
		// declaration is the cascade rule and stays allowed.
		layerOwner := make(map[string]string)
		for _, path := range entries {
			defs, err := c.loadEffectDocument(path, layer.plugin)
			if err != nil {
				return nil, err
			}
			for _, def := range defs {
				if prior, dup := layerOwner[def.ID]; dup {
					return nil, lang.DuplicateID(def.ID, prior, path)
				}
				layerOwner[def.ID] = path
				if layer.plugin {
					if owner, exists := pluginOwner[def.ID]; exists {
						return nil, fmt.Errorf("effect %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", def.ID, owner, path)
					}
					pluginOwner[def.ID] = path
					if layer.pluginID != "" {
						pluginOfSource[def.SourcePath] = layer.pluginID
					}
				}
				all = append(all, def)
				out[def.ID] = def
			}
		}
	}
	if err := resolveNestedDefinitions(out, all, pluginOfSource, func(ref string) string {
		return describeMissingPlugin(ref, c.catalogRegistrations, c.catalogLock, c.catalogCacheRoot)
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// workflowSearchDirs orders the cascade: plugins (base) → global → ancestors
// (outermost-first, ending at the workspace dir itself — the only untrusted
// layer).
func (c *Config) workflowSearchDirs(workspaceDirPath string) []layerDir {
	return c.searchDirs(workspaceDirPath, "workflows")
}

func (c *Config) tasksSearchDirs(workspaceDirPath string) []layerDir {
	return c.searchDirs(workspaceDirPath, "tasks")
}

func (c *Config) searchDirs(workspaceDirPath, kind string) []layerDir {
	pluginIDs := make(map[string]string, len(c.Plugins))
	for _, m := range c.Plugins {
		pluginIDs[m.Dir] = m.ID
	}
	var dirs []layerDir
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, layerDir{dir: filepath.Join(plugin, "config", kind), plugin: true, pluginID: pluginIDs[plugin]})
	}
	if c.BaseDir != "" {
		dirs = append(dirs, layerDir{dir: filepath.Join(c.BaseDir, kind)})
	}
	cleanWorkspaceDir := ""
	if workspaceDirPath != "" {
		cleanWorkspaceDir = filepath.Clean(workspaceDirPath)
	}
	for _, anc := range cascadeAncestors(workspaceDirPath) {
		dirs = append(dirs, layerDir{
			dir:          filepath.Join(anc, ".plect", kind),
			workspaceDir: anc == cleanWorkspaceDir,
		})
	}
	return dirs
}

// cascadeAncestors walks up from workspaceDirPath, ordered outermost-first.
// $HOME is the exclusive upper bound because the user's global config lives
// at `~/.config/plect/` and `$HOME/.plect/` would collide with that.
func cascadeAncestors(workspaceDirPath string) []string {
	if workspaceDirPath == "" {
		return nil
	}
	var cleanHome string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome = filepath.Clean(home)
	}
	var chain []string
	cur := filepath.Clean(workspaceDirPath)
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
	workspaceProviderSource := ""
	autoSelectSource := ""
	if merged.Name != "" {
		nameSource = layers[0].SourcePath
	}
	if merged.Description != "" {
		descSource = layers[0].SourcePath
	}
	if merged.WorkspaceProvider != "" {
		workspaceProviderSource = layers[0].SourcePath
	}
	if merged.AutoSelect != nil {
		autoSelectSource = layers[0].SourcePath
	}
	displaySource := ""
	if len(merged.Display) > 0 {
		displaySource = layers[0].SourcePath
	}
	wsInputsSource := ""
	if len(merged.WorkspaceProviderInputs) > 0 {
		wsInputsSource = layers[0].SourcePath
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
		if layer.WorkspaceProvider != "" {
			if workspaceProviderSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `workspace_provider` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, workspaceProviderSource, layer.SourcePath)
			}
			merged.WorkspaceProvider = layer.WorkspaceProvider
			workspaceProviderSource = layer.SourcePath
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
		if len(layer.WorkspaceProviderInputs) > 0 {
			if wsInputsSource != "" {
				return WorkflowFile{}, fmt.Errorf("workflow %q: `workspace_provider_inputs` is declared in both %s and %s; cascade layers may add new fields but cannot redeclare existing ones", merged.ID, wsInputsSource, layer.SourcePath)
			}
			merged.WorkspaceProviderInputs = layer.WorkspaceProviderInputs
			wsInputsSource = layer.SourcePath
		}
		// Runtime tuning tables are deeper-wins whole-table replacements, not
		// additive/no-redeclare like identity fields. A local trusted layer's
		// judgment should be free to override a global default outright.
		if layer.Tick != nil {
			merged.Tick = layer.Tick
		}
		if layer.Healthcheck != nil {
			merged.Healthcheck = layer.Healthcheck
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
		if len(key) == 1 && (key[0] == "environment" || key[0] == "environment_inputs") {
			return wf, fmt.Errorf("`%s` is retired along with the environment execution plane; see docs/migrations/", key[0])
		}
		if len(key) == 1 && key[0] == "done_when" {
			return wf, fmt.Errorf("workflow %s: `done_when` is retired at the workflow level; declare the completion predicate on the task definition's `[done_when]` instead", path)
		}
		if len(key) == 2 && key[0] == "tick" && key[1] == "stale_when" {
			return wf, fmt.Errorf("workflow %s: `tick.stale_when` was renamed to `tick.heartbeat`; update the workflow file", path)
		}
		if len(key) == 2 && key[0] == "tick" && key[1] == "max_stale_when" {
			return wf, fmt.Errorf("workflow %s: `tick.max_stale_when` was renamed to `tick.max_heartbeat`; update the workflow file", path)
		}
		// The session-scoped movement source is retired: an activity probe is
		// now a task-level `[health].activity` declaration the owning plugin
		// ships, so nothing about default health coverage is wired in
		// user-owned workflow config any more.
		if len(key) >= 2 && key[0] == "tick" && key[1] == "movement_source" {
			return wf, fmt.Errorf("workflow %s: `[tick.movement_source]` is retired; the task that owns the surface declares `[health].activity` instead; see docs/migrations/", path)
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

func validateStem(path string, re *regexp.Regexp, kind string) (string, error) {
	base := filepath.Base(path)
	stem := base[:len(base)-len(filepath.Ext(base))]
	if !re.MatchString(stem) {
		return "", fmt.Errorf("%s filename stem %q is not allowed (must match %s)", kind, stem, re.String())
	}
	return stem, nil
}
