package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

// Chain placement values: where the spawned workflow session attaches in the
// session tree relative to the work session that triggered it.
//
//   - sibling: an independent reviewer the parent spawns (the work session's
//     own parent becomes the reviewer's parent). Default.
//   - child:   a reviewer parented under the work session itself. Opt-in.
//
// These mirror the judge-leaf relation policy (domain.AssignableJudgeRelation):
// a sibling reviewer's verdict is accepted by the default `["sibling","parent"]`
// policy, a child reviewer's only when the leaf opts into `child`.
const (
	ChainPlacementSibling = "sibling"
	ChainPlacementChild   = "child"
)

// Chain judge-action vocabulary. These match the recorded judge action values
// (`plect judge approve` / `request-changes`); config validation rejects anything
// else so a `judge_action` fact can never silently never-match. Duplicated here
// rather than imported from the task package because task imports config, not
// the reverse.
const (
	chainJudgeActionApprove        = "approve"
	chainJudgeActionRequestChanges = "request_changes"
)

// chainIDRE constrains a chain id to the tag charset so it can be folded into a
// spawned reviewer's session tag without escaping.
var chainIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ChainDefinition is one `[[chains]]` entry: a declarative rule that spawns a
// predetermined workflow session when its trigger holds against a work
// session's raw facts.
//
//	[[chains]]
//	id        = "review"
//	workflow  = '{{if eq .Work.workflow "claude"}}codex{{else}}claude{{end}}'
//	placement = "sibling"
//
//	[chains.when]
//	all = [
//	  { judge_pending = "ac-met" },
//	  { check = "checks_status", in = ["SUCCESS", "NULL"] },
//	]
//
//	[chains.inputs]
//	revision  = "{{.Work.outputs.revision}}"
//	judge_ids = "{{.Work.done_when.pending_judge_ids}}"
//
// The chain fires when `when` holds AND every work output its `inputs` bindings
// reference is present (the firing gate). On a fire, the bindings are rendered
// against the work session's facts and validated: each `{{.Work.outputs.X}}`
// must be a published upstream output and the resolved inputs must satisfy the
// spawned workflow's inputs contract.
//
// TaskID names the task definition that declared this chain (stamped by
// validateTaskChains when a `[[chains]]` table is loaded from inside a task
// definition file). A chain is only ever evaluated against instances of this
// task (see TaskChains).
type ChainDefinition struct {
	ID string `toml:"id"`
	// Workflow is a Go template rendered against the same `.Work.*` vocabulary
	// as Inputs (chain.RenderWorkflow) — e.g. `{{if eq .Work.workflow
	// "claude"}}codex{{else}}claude{{end}}` picks the cross-tool reviewer. A
	// plain string (no template actions) renders to itself unchanged.
	Workflow   string            `toml:"workflow"`
	Placement  string            `toml:"placement"`
	When       ChainWhen         `toml:"when"`
	Inputs     map[string]string `toml:"inputs"`
	TaskID     string            `toml:"-"`
	SourcePath string            `toml:"-"`
}

// ChainWhen is the chain's trigger: a conjunction of raw-fact predicates.
type ChainWhen struct {
	All []ChainWhenFact `toml:"all"`
}

// ChainWhenFact is one trigger predicate, evaluated directly against raw facts
// (outputs / judge records / revision) — never a managed gate state. It is one
// of three kinds, exclusively:
//
//   - check: names an output and applies one comparison operator, exactly like
//     a done_when check leaf.
//   - judge_pending: names a judge leaf id; holds when that leaf has no usable
//     verdict at the current revision (missing / stale / self / wrong relation).
//   - judge_action: names a judge leaf id and the action (`is`) its current
//     verdict must carry.
type ChainWhenFact struct {
	Check string   `toml:"check"`
	Eq    *string  `toml:"eq"`
	Ne    *string  `toml:"ne"`
	In    []any    `toml:"in"`
	Gte   *float64 `toml:"gte"`
	Lte   *float64 `toml:"lte"`

	JudgePending string `toml:"judge_pending"`
	JudgeAction  string `toml:"judge_action"`
	Is           string `toml:"is"`
}

// EffectivePlacement returns the placement, defaulting to sibling.
func (c ChainDefinition) EffectivePlacement() string {
	if c.Placement == "" {
		return ChainPlacementSibling
	}
	return c.Placement
}

func (f ChainWhenFact) operatorCount() int {
	n := 0
	if f.Eq != nil {
		n++
	}
	if f.Ne != nil {
		n++
	}
	if f.In != nil {
		n++
	}
	if f.Gte != nil {
		n++
	}
	if f.Lte != nil {
		n++
	}
	return n
}

// Validate checks a chain's structural invariants.
func (c ChainDefinition) Validate() error {
	if !chainIDRE.MatchString(c.ID) {
		return fmt.Errorf("chain id %q is empty or has characters outside %s", c.ID, chainIDRE.String())
	}
	if strings.TrimSpace(c.Workflow) == "" {
		return fmt.Errorf("chain %q: `workflow` is required (the workflow a fire spawns)", c.ID)
	}
	if _, err := template.New("chain-workflow").Parse(c.Workflow); err != nil {
		return fmt.Errorf("chain %q workflow: %w", c.ID, err)
	}
	switch c.Placement {
	case "", ChainPlacementSibling, ChainPlacementChild:
	default:
		return fmt.Errorf("chain %q: placement %q is not %q or %q", c.ID, c.Placement, ChainPlacementSibling, ChainPlacementChild)
	}
	if len(c.When.All) == 0 {
		return fmt.Errorf("chain %q: `when.all` declares no facts; a chain with no trigger would fire unconditionally", c.ID)
	}
	for i, fact := range c.When.All {
		if err := fact.validate(); err != nil {
			return fmt.Errorf("chain %q when.all[%d]: %w", c.ID, i, err)
		}
	}
	for key, tmplStr := range c.Inputs {
		if _, err := template.New("chain-input").Parse(tmplStr); err != nil {
			return fmt.Errorf("chain %q input %q: %w", c.ID, key, err)
		}
	}
	return nil
}

func (f ChainWhenFact) validate() error {
	hasCheck := strings.TrimSpace(f.Check) != ""
	hasPending := strings.TrimSpace(f.JudgePending) != ""
	hasAction := strings.TrimSpace(f.JudgeAction) != ""
	kinds := 0
	for _, set := range []bool{hasCheck, hasPending, hasAction} {
		if set {
			kinds++
		}
	}
	switch {
	case kinds == 0:
		return fmt.Errorf("sets none of `check` / `judge_pending` / `judge_action`; exactly one is required")
	case kinds > 1:
		return fmt.Errorf("sets more than one of `check` / `judge_pending` / `judge_action`; exactly one is allowed")
	case hasCheck:
		if f.operatorCount() != 1 {
			return fmt.Errorf("check %q must set exactly one operator (eq/ne/in/gte/lte), got %d", f.Check, f.operatorCount())
		}
		if f.Is != "" {
			return fmt.Errorf("check leaf must not set `is`")
		}
	case hasPending:
		if f.operatorCount() > 0 {
			return fmt.Errorf("judge_pending must not set comparison operators")
		}
		if f.Is != "" {
			return fmt.Errorf("judge_pending must not set `is`")
		}
	case hasAction:
		if f.operatorCount() > 0 {
			return fmt.Errorf("judge_action must not set comparison operators")
		}
		switch f.Is {
		case chainJudgeActionApprove, chainJudgeActionRequestChanges:
		default:
			return fmt.Errorf("judge_action requires `is` to be %q or %q", chainJudgeActionApprove, chainJudgeActionRequestChanges)
		}
	}
	return nil
}

// ChainWiredOutputs returns the work-output keys a chain's input bindings
// reference via `{{.Work.outputs.<key>}}`, sorted and de-duplicated. Shared by
// the chain package's runtime firing-gate check (chain.WiredOutputs) and this
// package's load-time static wiring validation (validateTaskChains), so both
// read the same parse-tree scan of the binding templates.
func ChainWiredOutputs(inputs map[string]string) ([]string, error) {
	seen := map[string]bool{}
	var keys []string
	for _, tmplStr := range inputs {
		refs, err := chainOutputRefs(tmplStr)
		if err != nil {
			return nil, err
		}
		for _, k := range refs {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// chainOutputRefs scans a binding template for `.Work.outputs.<key>` field
// accesses. A static reference scan (not a render) so a missing output reads
// as "not yet wired" rather than rendering to an empty string.
func chainOutputRefs(tmplStr string) ([]string, error) {
	refs, err := TemplateFieldRefs(tmplStr)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ident := range refs {
		if len(ident) >= 3 && ident[0] == "Work" && ident[1] == "outputs" {
			out = append(out, ident[2])
		}
	}
	return out, nil
}

// validateTaskChains validates the `[[chains]]` embedded in a task definition
// against that same task's `done_when` and `outputs_schema` — the two static
// checks the ADR amendment requires at load time rather than at fire time:
//
//   - every `when` fact's judge_pending/judge_action id must name a judge leaf
//     declared in this task's done_when (a done_when-less task can declare no
//     judge-referencing chain).
//   - every `{{.Work.outputs.<key>}}` binding in `[chains.inputs]` must name a
//     property of this task's outputs_schema (skipped entirely when the task
//     declares no outputs_schema — there is then no contract to violate).
//
// It also stamps TaskID/SourcePath on each chain and rejects a chain id
// declared more than once within the same task file.
func validateTaskChains(def *TaskDefinition) error {
	if err := stampTaskChains(def); err != nil {
		return err
	}
	if len(def.Chains) == 0 {
		return nil
	}
	// An outer layer's chain resolves against the effective done_when and
	// public contract composed across the whole nesting chain, which is only
	// known once `inner` has been resolved — so a nested definition defers
	// its reference checks to resolveNestedDefinitions.
	if def.IsNested() {
		return nil
	}
	judgeIDs := make(map[string]bool)
	if def.DoneWhen != nil {
		for _, leaf := range def.DoneWhen.All {
			if leaf.ID != "" {
				judgeIDs[leaf.ID] = true
			}
		}
	}
	outputProps, err := SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
	if err != nil {
		return fmt.Errorf("task %q: outputs schema: %w", def.ID, err)
	}
	declaredOutputs := make(map[string]bool, len(outputProps))
	for _, p := range outputProps {
		declaredOutputs[p] = true
	}
	if err := validateChainReferences(*def, judgeIDs, declaredOutputs, false); err != nil {
		return fmt.Errorf("task %q: %w", def.ID, err)
	}
	return nil
}

// stampTaskChains records the declaring task on each chain and rejects a
// chain id declared twice in one file.
func unboundReason(composed bool) string {
	if composed {
		return "the composed public contract does not bind"
	}
	return "is not declared in this task's outputs_schema"
}

func stampTaskChains(def *TaskDefinition) error {
	seen := make(map[string]bool, len(def.Chains))
	for i := range def.Chains {
		ch := &def.Chains[i]
		ch.TaskID = def.ID
		ch.SourcePath = def.SourcePath
		if err := ch.Validate(); err != nil {
			return fmt.Errorf("task %q: %w", def.ID, err)
		}
		if seen[ch.ID] {
			return fmt.Errorf("task %q: chain id %q is declared more than once", def.ID, ch.ID)
		}
		seen[ch.ID] = true
	}
	return nil
}

// validateChainReferences resolves each chain's judge ids and output
// references against the judge namespace and public contract it was declared
// over — this task's own for a plain task, the composed ones for a layer of a
// nesting chain (composed). A chain fires against instances of the composed
// task and reads that task's public outputs, so every layer's chains answer
// to the composed contract, not to the layer's own.
func validateChainReferences(def TaskDefinition, judgeIDs, declaredOutputs map[string]bool, composed bool) error {
	for _, ch := range def.Chains {
		for j, fact := range ch.When.All {
			id := fact.JudgePending
			if id == "" {
				id = fact.JudgeAction
			}
			if id == "" {
				continue
			}
			if !judgeIDs[id] {
				return fmt.Errorf("chain %q when.all[%d]: judge id %q is not declared in this task's done_when", ch.ID, j, id)
			}
		}

		// A plain task with no outputs_schema has no contract to violate; a
		// layer of a nesting chain always has one, since the composed task
		// declares its whole public contract explicitly.
		if len(declaredOutputs) == 0 && !composed {
			continue
		}
		wired, err := ChainWiredOutputs(ch.Inputs)
		if err != nil {
			return fmt.Errorf("chain %q: %w", ch.ID, err)
		}
		for _, key := range wired {
			if !declaredOutputs[key] {
				return fmt.Errorf("chain %q: input binding wires output %q, which %s", ch.ID, key, unboundReason(composed))
			}
		}
		if !composed {
			continue
		}
		// The composed task declares its whole public contract explicitly, so
		// a chain fact reading anything else — a key outside the contract, or
		// a local the joint kept private — has no value to read at fire time.
		for j, fact := range ch.When.All {
			key := strings.TrimSpace(fact.Check)
			if key != "" && !declaredOutputs[key] {
				return fmt.Errorf("chain %q when.all[%d]: reads output %q, which %s", ch.ID, j, key, unboundReason(composed))
			}
		}
		locals, err := chainLocalRefs(ch.Inputs)
		if err != nil {
			return fmt.Errorf("chain %q: %w", ch.ID, err)
		}
		if len(locals) > 0 {
			return fmt.Errorf("chain %q: input binding reads local %q; locals are private to the joint, so bind the value into the public contract and read it as an output", ch.ID, locals[0])
		}
	}
	return nil
}

// LegacyChainsDirNotice reports one warning per surviving `chains/*.toml` file
// under the trusted base layers (plugin dirs, then BaseDir). The legacy
// dual-read (formerly LoadChains) is retired — TaskChains only reads
// task-embedded [[chains]] — so a file left behind here is silently inert;
// without this notice a migration straggler has zero signal that its rule
// stopped firing.
func (c *Config) LegacyChainsDirNotice() ([]string, error) {
	var dirs []string
	for _, plugin := range c.PluginDirs {
		dirs = append(dirs, filepath.Join(plugin, "chains"))
	}
	if c.BaseDir != "" {
		dirs = append(dirs, filepath.Join(c.BaseDir, "chains"))
	}
	var warnings []string
	for _, dir := range dirs {
		entries, err := listTOMLFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entries {
			warnings = append(warnings, fmt.Sprintf("%s is ignored: declare [[chains]] inside the task definition instead (the legacy chains/*.toml dual-read has been retired)", path))
		}
	}
	return warnings, nil
}

// TaskChains gathers every task-declared [[chains]] out of defs (as loaded by
// LoadTaskDefinitions) into one deterministically ordered list, sorted by
// TaskID then chain ID. Chains no longer have another source: the legacy
// `chains/*.toml` dual-read (LoadChains) was retired once every shipped and
// user chain migrated into its owning task definition.
func TaskChains(defs map[string]TaskDefinition) []ChainDefinition {
	taskIDs := make([]string, 0, len(defs))
	for id := range defs {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)

	var out []ChainDefinition
	for _, taskID := range taskIDs {
		def := defs[taskID]
		chainIDs := make([]string, 0, len(def.Chains))
		byID := make(map[string]ChainDefinition, len(def.Chains))
		for _, ch := range def.Chains {
			chainIDs = append(chainIDs, ch.ID)
			byID[ch.ID] = ch
		}
		sort.Strings(chainIDs)
		for _, id := range chainIDs {
			out = append(out, byID[id])
		}
	}
	return out
}
