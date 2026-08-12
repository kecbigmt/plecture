// Package task implements the declarative setup/cleanup orchestrator.
// Each task is a setup/cleanup pair declared in
// config.toml, scoped to either the session lifecycle ("session") or the
// run lifecycle ("run"). Tasks can depend on each other, and outputs from
// a setup command (parsed as JSON from stdout) are exposed to dependents
// and the task's own cleanup via Go templates.
//
// The runner is intentionally minimal: sequential execution, no reactivity,
// no dynamic DAG. Scope-aware topological sort, single-pass template render
// at cmd dispatch, and persisted state via contracts/state.TaskState.
package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/state"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Resolved represents a workflow node after compilation: a task definition
// instantiated under a node id, with its input bindings, depends_on edges
// derived from those bindings, and the compiled outputs schema.
//
// NodeID is the state key (the field used to index session.Tasks). TaskID
// is the `uses` target — preserved for traceability and to render `.TaskID`
// in templates when a node id has been customized.
type Resolved struct {
	NodeID        string
	TaskID        string
	Scope         string // canonical scope ("session" | "run")
	Setup         string
	Cleanup       string
	Healthcheck   string
	Primary       bool
	Attach        string
	Capture       string
	IdleAfter     config.Duration
	Inputs        map[string]string  // template strings rendered at setup time → .Input.<key>
	InputsSchema  *jsonschema.Schema // optional: validated against resolved inputs
	OutputsSchema *jsonschema.Schema
	// MutableOutputs lists output keys declared `mutable = true` in the
	// outputs schema. Only these may be updated post-setup via
	// `plect state set-output`; everything else is immutable (safe by default).
	MutableOutputs []string
	DependsOn      []string
	// DoneWhen is the task's per-instance Definition of Done;
	// nil for pure lifecycle-only tasks. Evaluated against the instance's own
	// outputs for `plect status` / `ls` display.
	DoneWhen *config.DoneWhen
	// Execution is the resolved execution plane ("host" or "environment") —
	// always one of those two after ResolveExecution runs, never the raw
	// possibly-empty TaskDefinition.Execution. CompileWorkflow resolves it for
	// static nodes; the dynamic `plect task setup` path resolves it separately
	// (ResolveDefinition has no workflow to consult).
	Execution string
}

// Plan groups tasks by scope, in topo-sorted order.
type Plan struct {
	Session []Resolved // session-scoped nodes, setup order
	Run     []Resolved // run-scoped nodes, setup order
}

// AttachTask returns the resolved node that declares attach, or nil if
// none does. Validate has already enforced at most one such declaration.
func (p *Plan) AttachTask() *Resolved {
	for i, r := range p.Session {
		if r.Attach != "" {
			return &p.Session[i]
		}
	}
	for i, r := range p.Run {
		if r.Attach != "" {
			return &p.Run[i]
		}
	}
	return nil
}

// CaptureTask returns the resolved node that declares capture, or nil if none
// does. Unlike attach (validated to at most one at compile time, see
// assemblePlan), any number of task definitions may declare capture — a
// session simply never resolves to one until `plect capture` is called on it —
// so ambiguity across the resolved plan is reported here as an error instead.
func (p *Plan) CaptureTask() (*Resolved, error) {
	var found []*Resolved
	for i, r := range p.Session {
		if r.Capture != "" {
			found = append(found, &p.Session[i])
		}
	}
	for i, r := range p.Run {
		if r.Capture != "" {
			found = append(found, &p.Run[i])
		}
	}
	if len(found) > 1 {
		ids := make([]string, len(found))
		for i, r := range found {
			ids[i] = r.NodeID
		}
		return nil, fmt.Errorf("more than one node declares capture: %s (ambiguous; at most one may be resolved per session)", strings.Join(ids, ", "))
	}
	if len(found) == 0 {
		return nil, nil
	}
	return found[0], nil
}

// RenderAttach expands the attach template against the task's own outputs
// and session vars (.Self / .SessionName / .WorktreePath / .ResourceID / ...).
// Uses the same strict missingkey semantics as setup — an unset .Self.<key>
// is a contract violation, surfaced as an error instead of an empty arg.
func RenderAttach(cmd string, selfOutputs map[string]any, session SessionVars) (string, error) {
	return render(cmd, RenderContext{Self: selfOutputs, Session: session})
}

// RunHealthcheck renders cmd against the task's own outputs, its resolved
// node inputs (persisted at TaskState.Inputs — needed when a healthcheck
// re-derives a mutable output from an input like tmux_session, not just
// .Self), and session vars (mirroring RenderAttach). Runs via bash -c. A
// non-zero exit or a render failure is returned as an error carrying stderr;
// nil means the probe reported healthy.
func RunHealthcheck(goCtx context.Context, cmd string, selfOutputs map[string]any, nodeInputs map[string]any, session SessionVars) error {
	rendered, err := render(cmd, RenderContext{Self: selfOutputs, Inputs: nodeInputs, Session: session})
	if err != nil {
		return err
	}
	_, stderr, err := execHostScript(goCtx, rendered, session.WorktreePath)
	if err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
		}
		return err
	}
	return nil
}

// RunCapture renders the capture template against the task's own outputs and
// session vars (mirroring RenderAttach) and runs it via bash -c, returning
// stdout as-is — this is a read-only "raw view", not an outputs contract, so
// stdout is never JSON-parsed. A non-zero exit or a render failure is
// returned as an error carrying stderr (e.g. a pane that no longer exists),
// so an orphaned channel never succeeds with empty output.
func RunCapture(goCtx context.Context, cmd string, selfOutputs map[string]any, session SessionVars) (string, error) {
	rendered, err := render(cmd, RenderContext{Self: selfOutputs, Session: session})
	if err != nil {
		return "", err
	}
	stdout, stderr, err := execHostScript(goCtx, rendered, session.WorktreePath)
	if err != nil {
		if len(stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
		}
		return "", err
	}
	return string(stdout), nil
}

// CompileWorkflow turns a workflow file plus its referenced task definitions
// into an executable Plan. The DAG is derived from `.Nodes.<id>.outputs.<key>`
// references in node input bindings (no explicit depends_on).
//
// Errors are surfaced for:
//   - duplicate node ids
//   - `uses` referencing an unknown task definition
//   - input bindings that reference a missing node
//   - input bindings that reference a non-existent output key (when the
//     upstream task declares an outputs schema)
//   - cycles in the derived DAG
//   - session-scoped nodes depending on run-scoped nodes
//   - more than one node declaring `attach`
func CompileWorkflow(wf config.WorkflowFile, defs map[string]config.TaskDefinition) (*Plan, error) {
	if len(wf.Nodes) == 0 {
		// An empty plan would silently no-op `plect up --workflow foo`, which
		// is more confusing than helpful. Force the author to either declare
		// nodes or delete the file.
		return nil, fmt.Errorf("workflow %q declares no nodes", wf.ID)
	}
	resolved, err := resolveWorkflowNodes(wf, defs)
	if err != nil {
		return nil, err
	}
	return assemblePlan(resolved)
}

func resolveWorkflowNodes(wf config.WorkflowFile, defs map[string]config.TaskDefinition) ([]Resolved, error) {
	out := make([]Resolved, 0, len(wf.Nodes))
	for _, node := range wf.Nodes {
		if node.ID == "" && node.Uses == "" {
			return nil, fmt.Errorf("workflow %q: node must declare at least one of `id` or `uses`", wf.ID)
		}
		// `uses` is the primary field (GHA Steps convention); `id` is a
		// per-instance label that only needs to be spelled out when the same
		// task is instantiated multiple times in one workflow. Since task
		// filenames are constrained to nodeIDRE, the defaulted node id is
		// guaranteed to be a valid Go template identifier.
		nodeID := node.ID
		if nodeID == "" {
			nodeID = node.Uses
		}
		if !nodeIDRE.MatchString(nodeID) {
			// Reject hyphens etc. up front. Otherwise the user discovers the
			// problem at `plect up` time via a template parse error inside a
			// downstream node's input binding.
			return nil, fmt.Errorf("workflow %q: node id %q is not a valid Go template identifier (must match %s); rename it to use underscores", wf.ID, nodeID, nodeIDRE.String())
		}
		uses := node.Uses
		if uses == "" {
			uses = nodeID
		}
		def, ok := defs[uses]
		if !ok {
			return nil, fmt.Errorf("workflow %q: node %q references unknown task %q", wf.ID, nodeID, uses)
		}
		resolved, err := ResolveDefinition(def, nodeID)
		if err != nil {
			return nil, err
		}
		resolved.Inputs = cloneInputs(node.Inputs)
		resolvedExecution, err := ResolveExecution(resolved.Execution, wf.Environment)
		if err != nil {
			return nil, fmt.Errorf("workflow %q: node %q: %w", wf.ID, nodeID, err)
		}
		resolved.Execution = resolvedExecution
		out = append(out, resolved)
	}
	if err := deriveDependsOn(out); err != nil {
		return nil, err
	}
	if err := applyBlocks(out, wf.Nodes); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveDefinition compiles a single task definition into a Resolved under
// the given node id: scope validation, input/output schema compilation, mutable
// key extraction, and done_when template validation. It does not populate
// workflow-specific fields (Inputs / DependsOn) — the workflow compiler sets
// those, and the dynamic `plect task setup` path leaves them empty (it binds
// input values directly rather than via node templates).
func ResolveDefinition(def config.TaskDefinition, nodeID string) (Resolved, error) {
	scope := def.EffectiveScope()
	if scope != config.TaskScopeSession && scope != config.TaskScopeRun {
		return Resolved{}, fmt.Errorf("task %q: invalid scope %q (want %q or %q)",
			def.ID, def.Scope, config.TaskScopeSession, config.TaskScopeRun)
	}
	outputsSchema, err := CompileSchema(def.OutputsSchema, def.ResolvedOutputsSchemaPath(), "plect:task:"+def.ID+":outputs")
	if err != nil {
		return Resolved{}, fmt.Errorf("task %q: outputs schema: %w", def.ID, err)
	}
	mutableOutputs, err := MutableOutputKeys(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
	if err != nil {
		return Resolved{}, fmt.Errorf("task %q: outputs schema: %w", def.ID, err)
	}
	inputsSchema, err := CompileSchema(def.InputsSchema, def.ResolvedInputsSchemaPath(), "plect:task:"+def.ID+":inputs")
	if err != nil {
		return Resolved{}, fmt.Errorf("task %q: input schema: %w", def.ID, err)
	}
	if err := def.DoneWhen.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("task %q: %w", def.ID, err)
	}
	if err := validateTaskRequires(def); err != nil {
		return Resolved{}, fmt.Errorf("task %q: %w", def.ID, err)
	}
	if err := config.ValidateDynamicOutputs(def.DynamicOutputs); err != nil {
		return Resolved{}, fmt.Errorf("task %q: %w", def.ID, err)
	}
	return Resolved{
		NodeID:         nodeID,
		TaskID:         def.ID,
		Scope:          scope,
		Setup:          def.Setup,
		Cleanup:        def.Cleanup,
		Healthcheck:    def.Healthcheck,
		Primary:        def.Primary,
		Attach:         def.Attach,
		Capture:        def.Capture,
		IdleAfter:      def.IdleAfter,
		InputsSchema:   inputsSchema,
		OutputsSchema:  outputsSchema,
		MutableOutputs: mutableOutputs,
		DoneWhen:       def.DoneWhen,
		// Execution starts as the raw declared value (possibly empty); callers
		// resolve it against the workflow's Environment via ResolveExecution.
		// CompileWorkflow does so for static nodes; the dynamic `plect task
		// setup` path (which has no Resolved.Inputs/DependsOn either) does the
		// same against the session's frozen workflow.
		Execution: def.Execution,
	}, nil
}

// ResolveExecution resolves a task's declared `execution` against the
// workflow's `environment`: "" defaults to "environment" when the workflow
// declares a non-host environment, else "host"; an explicit "host" or
// "environment" is returned as-is, except "environment" on a workflow with
// no environment is an error — that combination can never actually run in
// the environment plane, so failing at compile time beats a silent host
// fallback.
func ResolveExecution(declared, workflowEnvironment string) (string, error) {
	hasEnvironment := workflowEnvironment != "" && workflowEnvironment != config.ExecutionHost
	switch declared {
	case "":
		if hasEnvironment {
			return config.ExecutionEnvironment, nil
		}
		return config.ExecutionHost, nil
	case config.ExecutionHost:
		return config.ExecutionHost, nil
	case config.ExecutionEnvironment:
		if !hasEnvironment {
			return "", fmt.Errorf("execution = %q but the workflow declares no environment", config.ExecutionEnvironment)
		}
		return config.ExecutionEnvironment, nil
	default:
		return "", fmt.Errorf("invalid execution %q (want %q or %q)", declared, config.ExecutionHost, config.ExecutionEnvironment)
	}
}

// applyBlocks turns each WorkflowNode.Blocks entry into a reverse dependency
// edge: if node A lists B in Blocks, B gains A as a dependency, forcing A to
// set up before B (and cleanup after B in reverse order). Scope violations and
// cycles are caught later by assemblePlan's existing checks.
func applyBlocks(nodes []Resolved, wfNodes []config.WorkflowNode) error {
	byID := make(map[string]*Resolved, len(nodes))
	for i := range nodes {
		byID[nodes[i].NodeID] = &nodes[i]
	}
	if len(wfNodes) != len(nodes) {
		return fmt.Errorf("internal: applyBlocks length mismatch (%d wfNodes vs %d resolved)", len(wfNodes), len(nodes))
	}
	for i, wfn := range wfNodes {
		if len(wfn.Blocks) == 0 {
			continue
		}
		srcID := nodes[i].NodeID
		for _, target := range wfn.Blocks {
			if target == srcID {
				return fmt.Errorf("node %q: blocks itself", srcID)
			}
			t, ok := byID[target]
			if !ok {
				return fmt.Errorf("node %q: blocks unknown node %q", srcID, target)
			}
			if hasDep(t.DependsOn, srcID) {
				continue
			}
			t.DependsOn = append(t.DependsOn, srcID)
		}
	}
	return nil
}

func hasDep(deps []string, id string) bool {
	for _, d := range deps {
		if d == id {
			return true
		}
	}
	return false
}

func cloneInputs(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// nodeRefRE matches `{{ ... .Nodes.<id>.outputs ... }}` references in a node
// input template. Captures the node id.
//
// Node ids are restricted to Go template field syntax (`[A-Za-z_][A-Za-z0-9_]*`)
// because text/template parses `.Nodes.foo.outputs` as a chain of dotted field
// accesses, and `-` would be lexed as the subtraction operator. Hyphenated
// task ids are still fine (referenced via `uses = "..."` in TOML, not in
// templates) — only the node id used as a template lookup key needs this.
var nodeIDRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var nodeRefRE = regexp.MustCompile(`\{\{[^}]*\.Nodes\.([A-Za-z_][A-Za-z0-9_]*)\.outputs`)

// deriveDependsOn fills each node's DependsOn slice based on `.Nodes.<id>.outputs`
// references in its inputs. Validates that referenced nodes exist and that
// scope ordering is respected (session must not depend on run).
func deriveDependsOn(nodes []Resolved) error {
	byID := make(map[string]*Resolved, len(nodes))
	for i := range nodes {
		byID[nodes[i].NodeID] = &nodes[i]
	}
	for i := range nodes {
		n := &nodes[i]
		seen := make(map[string]bool)
		for inputKey, tmpl := range n.Inputs {
			matches := nodeRefRE.FindAllStringSubmatch(tmpl, -1)
			for _, m := range matches {
				ref := m[1]
				if ref == n.NodeID {
					return fmt.Errorf("node %q input %q references itself", n.NodeID, inputKey)
				}
				dep, ok := byID[ref]
				if !ok {
					return fmt.Errorf("node %q input %q references unknown node %q", n.NodeID, inputKey, ref)
				}
				if n.Scope == config.TaskScopeSession && dep.Scope == config.TaskScopeRun {
					return fmt.Errorf("node %q (session) depends on %q (run): session-scoped nodes must not depend on run-scoped nodes", n.NodeID, ref)
				}
				if !seen[ref] {
					n.DependsOn = append(n.DependsOn, ref)
					seen[ref] = true
				}
			}
		}
	}
	return nil
}

func assemblePlan(resolved []Resolved) (*Plan, error) {
	byID := make(map[string]Resolved, len(resolved))
	for _, r := range resolved {
		if _, dup := byID[r.NodeID]; dup {
			return nil, fmt.Errorf("duplicate node id %q", r.NodeID)
		}
		byID[r.NodeID] = r
	}

	// At most one attach declaration per plan.
	var attachID string
	for _, r := range resolved {
		if r.Attach == "" {
			continue
		}
		if attachID != "" {
			return nil, fmt.Errorf("more than one node declares attach: %q and %q (at most one allowed)", attachID, r.NodeID)
		}
		attachID = r.NodeID
	}

	// Validate DependsOn references and scope rule.
	for _, r := range resolved {
		for _, dep := range r.DependsOn {
			d, ok := byID[dep]
			if !ok {
				return nil, fmt.Errorf("node %q depends on unknown node %q", r.NodeID, dep)
			}
			if r.Scope == config.TaskScopeSession && d.Scope == config.TaskScopeRun {
				return nil, fmt.Errorf("node %q (session) depends on %q (run): session-scoped nodes must not depend on run-scoped nodes", r.NodeID, dep)
			}
		}
	}

	sessionList := filterByScope(resolved, config.TaskScopeSession)
	runList := filterByScope(resolved, config.TaskScopeRun)

	sessionSorted, err := topoSortNodes(sessionList)
	if err != nil {
		return nil, err
	}
	runSorted, err := topoSortNodes(runList)
	if err != nil {
		return nil, err
	}

	return &Plan{Session: sessionSorted, Run: runSorted}, nil
}

func filterByScope(resolved []Resolved, scope string) []Resolved {
	var out []Resolved
	for _, r := range resolved {
		if r.Scope == scope {
			out = append(out, r)
		}
	}
	return out
}

// CompileSchema returns (nil, nil) when neither input is set; both set is an error.
func CompileSchema(inline map[string]any, filePath, inlineID string) (*jsonschema.Schema, error) {
	hasInline := len(inline) > 0
	hasFile := filePath != ""
	if hasInline && hasFile {
		return nil, fmt.Errorf("inline schema and schema file are mutually exclusive")
	}
	switch {
	case hasInline:
		// TOML decodes ints as int64; round-trip through JSON so the
		// validator sees the same shape it would from a .json file.
		raw, err := json.Marshal(inline)
		if err != nil {
			return nil, fmt.Errorf("marshal inline schema: %w", err)
		}
		return compileSchemaBytes(inlineID, raw)
	case hasFile:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filePath, err)
		}
		return compileSchemaBytes(filePath, data)
	}
	return nil, nil
}

func compileSchemaBytes(id string, raw []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", id, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("add %s: %w", id, err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", id, err)
	}
	return schema, nil
}

// topoSortNodes returns nodes in dependency-respecting order using Kahn's
// algorithm. Dependencies pointing outside `list` are treated as already
// satisfied — run-scoped nodes can depend on session-scoped nodes that live
// in a separate plan.
func topoSortNodes(list []Resolved) ([]Resolved, error) {
	indeg := make(map[string]int, len(list))
	inScope := make(map[string]bool, len(list))
	for _, r := range list {
		inScope[r.NodeID] = true
	}
	for _, r := range list {
		for _, dep := range r.DependsOn {
			if !inScope[dep] {
				continue
			}
			indeg[r.NodeID]++
		}
	}

	// Preserve declaration order among equally-ready nodes for stable output.
	var ready []Resolved
	for _, r := range list {
		if indeg[r.NodeID] == 0 {
			ready = append(ready, r)
		}
	}

	var out []Resolved
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		out = append(out, cur)
		for _, r := range list {
			for _, dep := range r.DependsOn {
				if dep != cur.NodeID {
					continue
				}
				indeg[r.NodeID]--
				if indeg[r.NodeID] == 0 {
					ready = append(ready, r)
				}
			}
		}
	}

	if len(out) != len(list) {
		for _, r := range list {
			if indeg[r.NodeID] > 0 {
				return nil, fmt.Errorf("task dependency cycle detected (involves %q)", r.NodeID)
			}
		}
		return nil, fmt.Errorf("task dependency cycle detected")
	}
	return out, nil
}

// RenderContext is the variable bundle for task templates.
//
//	Self     — current node's outputs (cleanup only).
//	Prev     — previous run's outputs (setup; empty on first run / post-destroy).
//	Tasks  — outputs of upstream nodes, indexed by node id.
//	Inputs   — node's resolved input bindings (post-render), exposed as `.Inputs.<key>`.
//	           Also reachable via `.Nodes.<self>.inputs.<key>` for symmetry.
//	Workflow — the workflow pseudo-node's outputs, exposed as
//	           `.Workflow.outputs.<key>` (workdir, branch, ...). Empty for
//	           sessions whose workflow declares no setup hook.
//	Environment — the environment pseudo-node's outputs, exposed as
//	           `.Environment.outputs.<key>`. Empty for sessions whose
//	           workflow declares no environment (host degeneration) or whose
//	           environment declares no setup.
//
// `.Nodes.<id>.outputs.<key>` is the canonical reference for upstream node
// outputs in setup/cleanup templates; `.Tasks.<id>.<key>` is a shorter
// alias that resolves to the same value.
type RenderContext struct {
	Self        map[string]any
	Prev        map[string]any
	Tasks       map[string]map[string]any
	Inputs      map[string]any
	Workflow    map[string]any
	Environment map[string]any
	Session     SessionVars
}

type SessionVars struct {
	Name          string
	ResourceID    string
	ParentSession string
	WorktreePath  string
	Branch        string
	Inputs        map[string]any
}

var templateFuncs = template.FuncMap{
	// get reads a key from a string-keyed map, returning "" if missing.
	// Lets `.Prev` be safely read under setup's strict missingkey=error.
	"get": func(m map[string]any, key string) any {
		if m == nil {
			return ""
		}
		if v, ok := m[key]; ok && v != nil {
			return v
		}
		return ""
	},
	// shellQuote renders a value as a single-quoted POSIX shell word, so a
	// hook author can interpolate a resource id, session name, or persisted
	// output into a command string without the shell that runs it treating
	// embedded quotes, semicolons, or command substitution as syntax. Every
	// value crossing this template boundary is attacker-influenced at some
	// remove (resource ids and session tags both come from create's caller),
	// so a hook command must quote each templated value it interpolates
	// rather than rely on surrounding literal quotes in the command string.
	"shellQuote": func(v any) string {
		return "'" + strings.ReplaceAll(fmt.Sprint(v), "'", `'\''`) + "'"
	},
}

// normalizeNumbers converts integer-valued float64 entries to int64. JSON
// unmarshal into map[string]any leaves every number as float64; templates
// render large float64 as scientific notation (e.g. 3.052179e+06), which
// breaks scripts that compare the rendered value as a string (`pid` etc.).
// Walks nested maps and slices so deep outputs are covered too.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeNumbers(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeNumbers(vv)
		}
		return out
	}
	return v
}

func normalizeOutputs(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out, _ := normalizeNumbers(m).(map[string]any)
	return out
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// workflowOutputs extracts the workflow pseudo-node's outputs from the
// persisted tasks map (empty when the workflow declares no setup hook).
func workflowOutputs(tasks map[string]*contract.TaskState) map[string]any {
	if st, ok := tasks[contract.WorkflowPseudoNodeID]; ok && st != nil {
		return st.Outputs
	}
	return nil
}

// environmentOutputs extracts the environment pseudo-node's outputs from the
// persisted tasks map (empty when the workflow declares no environment, or
// the environment declares no setup).
func environmentOutputs(tasks map[string]*contract.TaskState) map[string]any {
	if st, ok := tasks[contract.EnvironmentPseudoNodeID]; ok && st != nil {
		return st.Outputs
	}
	return nil
}

// render is the setup-template renderer (missingkey=error: a missing
// dependency is a contract violation, surface it).
func render(cmd string, ctx RenderContext) (string, error) {
	return renderWith(cmd, ctx, "missingkey=error")
}

// renderCleanup is the cleanup-template renderer. Missing keys render as ""
// so a partial setup can still be torn down by shell-defensive scripts
// (`kill ... || exit 0` etc).
//
// Go renders nil interface{} as "<no value>", which would inject that
// literal into the shell (e.g. `<` becomes input redirection); strip it.
func renderCleanup(cmd string, ctx RenderContext) (string, error) {
	out, err := renderWith(cmd, ctx, "missingkey=zero")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(out, "<no value>", ""), nil
}

func renderWith(cmd string, ctx RenderContext, opt string) (string, error) {
	tmpl, err := template.New("task").
		Option(opt).
		Funcs(templateFuncs).
		Parse(cmd)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	// Per-node view for `.Nodes.<id>.outputs.<key>` references in input bindings
	// (DAG derivation) and setup/cleanup templates. Built from the same
	// dependency outputs that `.Tasks.<id>.<key>` exposes so the two names
	// resolve to identical values.
	tasks := map[string]map[string]any{}
	nodes := map[string]map[string]any{}
	for k, v := range ctx.Tasks {
		normalized := normalizeOutputs(v)
		tasks[k] = normalized
		nodes[k] = map[string]any{"outputs": normalized}
	}
	// `.Input` is strictly the node's own resolved inputs. Session-level
	// input is reachable as `.SessionInput.<key>` so the meaning of `.Input`
	// is identical whether the template runs as a node-input binding or as a
	// setup/cleanup body.
	data := struct {
		Self          map[string]any
		Prev          map[string]any
		Tasks         map[string]map[string]any
		Nodes         map[string]map[string]any
		Inputs        map[string]any
		Workflow      map[string]any
		Environment   map[string]any
		SessionInputs map[string]any
		SessionName   string
		ResourceID    string
		ParentSession string
		WorktreePath  string
		Branch        string
	}{
		Self:          normalizeOutputs(ctx.Self),
		Prev:          normalizeOutputs(ctx.Prev),
		Tasks:         tasks,
		Nodes:         nodes,
		Inputs:        normalizeOutputs(ctx.Inputs),
		Workflow:      map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))},
		Environment:   map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Environment))},
		SessionInputs: normalizeOutputs(ctx.Session.Inputs),
		SessionName:   ctx.Session.Name,
		ResourceID:    ctx.Session.ResourceID,
		ParentSession: ctx.Session.ParentSession,
		WorktreePath:  ctx.Session.WorktreePath,
		Branch:        ctx.Session.Branch,
	}
	if data.Self == nil {
		data.Self = map[string]any{}
	}
	if data.Prev == nil {
		data.Prev = map[string]any{}
	}
	if data.Tasks == nil {
		data.Tasks = map[string]map[string]any{}
	}
	if data.Nodes == nil {
		data.Nodes = map[string]map[string]any{}
	}
	if data.Inputs == nil {
		data.Inputs = map[string]any{}
	}
	if data.SessionInputs == nil {
		data.SessionInputs = map[string]any{}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return buf.String(), nil
}

// RenderInputs renders each node-input template against upstream node outputs,
// the workflow pseudo-node outputs, and the session vars. Used at setup time
// so the resulting map can be persisted to TaskState.Inputs and exposed to
// setup as `.Input.<key>`.
//
// envOutputs is optional (variadic so every pre-existing call site keeps
// compiling unchanged): the environment pseudo-node's outputs, exposed as
// `.Environment.outputs.<key>` alongside `.Workflow.outputs.<key>`.
//
// Inputs are rendered eagerly with missingkey=error so a typo in a reference
// fails fast rather than producing a silent empty value.
func RenderInputs(inputs map[string]string, deps map[string]map[string]any, wfOutputs map[string]any, session SessionVars, envOutputs ...map[string]any) (map[string]any, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(inputs))
	ctx := RenderContext{
		Tasks:    deps,
		Workflow: wfOutputs,
		Session:  session,
	}
	if len(envOutputs) > 0 {
		ctx.Environment = envOutputs[0]
	}
	for k, tmpl := range inputs {
		v, err := render(tmpl, ctx)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// ParseOutputs parses a task setup's stdout as JSON. Empty input is treated
// as an empty object. Parse failure returns an error so the caller can mark
// the task as failed (contract violation).
//
// The contract requires a JSON *object*. A literal `null` unmarshals into a
// nil map without error, so we reject it explicitly — silently treating it
// as `{}` would mask a misbehaving setup script.
func ParseOutputs(stdout []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, fmt.Errorf("setup stdout is not a JSON object: %w", err)
	}
	if out == nil {
		return nil, fmt.Errorf("setup stdout is not a JSON object: got null")
	}
	return out, nil
}

// dependencyOutputs builds the .Tasks.<id> / .Nodes.<id>.outputs map for the
// given node from the persisted TaskState entries, restricted to declared
// dependencies.
func dependencyOutputs(deps []string, tasks map[string]*contract.TaskState) map[string]map[string]any {
	out := make(map[string]map[string]any, len(deps))
	for _, dep := range deps {
		if state, ok := tasks[dep]; ok && state != nil {
			if state.Outputs != nil {
				out[dep] = state.Outputs
			} else {
				out[dep] = map[string]any{}
			}
		}
	}
	return out
}

// firstExecutor returns the first (only meaningful) element of a variadic
// Executor slice, or nil — the shared helper behind every RunSetup/RunCleanup/
// ExecuteTaskSetup call site that accepts an optional envExecutor.
func firstExecutor(envExecutor []Executor) Executor {
	if len(envExecutor) > 0 {
		return envExecutor[0]
	}
	return nil
}

// execForNode runs cmdStr through the node's resolved execution plane. Host
// always runs via execHostScript. Environment requires a non-nil envExecutor
// — a nil envExecutor (the workflow's environment setup failed, was never
// produced, or the caller never resolved one) is a fail-closed error, NOT a
// silent fallback to host: a node declaring execution="environment" must
// never run outside the environment it asked for, since a caller might rely
// on that isolation for credentials, tools, or a container-only filesystem.
func execForNode(goCtx context.Context, execution string, envExecutor Executor, cmdStr, workDir string) (stdout, stderr []byte, err error) {
	if execution == config.ExecutionEnvironment {
		if envExecutor == nil {
			return nil, nil, fmt.Errorf("execution = %q but no environment executor is available (environment setup may have failed, not run yet, or been torn down)", config.ExecutionEnvironment)
		}
		return envExecutor.Run(goCtx, ExecRequest{Argv: []string{"bash", "-c", cmdStr}, Dir: workDir})
	}
	return execHostScript(goCtx, cmdStr, workDir)
}

// runShell executes the given (already-rendered) command via "bash -c" on
// the host — always, regardless of any workflow's Environment (see
// alwaysHostExecutor). stdout is captured (parsed as JSON outputs); stderr is
// captured separately so the caller can decide when to surface it —
// streaming it during the run would interleave with the progress spinner.
// If workDir is non-empty and exists, it is used as the command's cwd.
// runShell's callers (provider setup/cleanup/subscribe, workflow and
// environment hooks, resource observe/finalize) have no caller-supplied
// context to thread through yet, so it always runs with context.Background();
// giving those paths a cancellable context is separate follow-up work.
func runShell(cmdStr, workDir string) (stdout, stderr []byte, err error) {
	return alwaysHostExecutor.Run(context.Background(), ExecRequest{Argv: []string{"bash", "-c", cmdStr}, Dir: workDir})
}

// Observer receives lifecycle events from RunSetup / RunCleanup. The runner
// only emits "what happened"; observers decide what to do with the events —
// render a progress UI, ship to telemetry, write a structured log, etc.
// A nil observer is treated as no-op.
//
// Events fire once per task, in execution order. OnStart precedes either
// OnSuccess or OnFailure; OnSkip is the alternative when an already-produced
// task is short-circuited (no OnStart for skipped tasks).
//
// OnSuccess and OnFailure receive the script's captured stderr so observers
// can surface diagnostic output (warnings, error context) without it racing
// against an animated spinner mid-run. The slice is nil when no script ran
// (e.g. empty setup body or template-render failures before exec).
type Observer interface {
	OnStart(scope, id string)
	OnSkip(scope, id, reason string)
	OnSuccess(scope, id string, elapsed time.Duration, stderr []byte)
	OnFailure(scope, id string, elapsed time.Duration, err error, stderr []byte)
}

type nopObserver struct{}

func (nopObserver) OnStart(string, string)                                 {}
func (nopObserver) OnSkip(string, string, string)                          {}
func (nopObserver) OnSuccess(string, string, time.Duration, []byte)        {}
func (nopObserver) OnFailure(string, string, time.Duration, error, []byte) {}

func observerOr(o Observer) Observer {
	if o == nil {
		return nopObserver{}
	}
	return o
}

// RunSetup executes the setup commands for the provided ordered task list
// against the given session. Outputs are persisted into session.Tasks.
// Stops at the first failure; subsequent tasks in the slice are not run.
//
// RunSetup is idempotent: a task whose persisted state is already
// "produced" is skipped, so lifecycle commands (create / up) can be safely
// retried after a partial failure. Tasks in any other state (absent,
// "failed", "cleaned") are re-run with a fresh setup attempt. Task authors
// must make their setup scripts cope with this by verifying the desired
// state rather than blindly recreating; see README "Task model" section.
//
// envExecutor is optional (variadic so every pre-existing call site keeps
// compiling unchanged): when supplied, a node whose resolved Execution is
// "environment" runs through it instead of the host.
func RunSetup(goCtx context.Context, ordered []Resolved, session SessionVars, tasks map[string]*contract.TaskState, observer Observer, envExecutor ...Executor) error {
	obs := observerOr(observer)
	ee := firstExecutor(envExecutor)
	for _, r := range ordered {
		if existing, ok := tasks[r.NodeID]; ok && existing != nil && existing.Status == contract.TaskStatusProduced {
			obs.OnSkip(r.Scope, r.NodeID, "already produced")
			continue
		}
		obs.OnStart(r.Scope, r.NodeID)
		now := time.Now()
		// Cleanup flips Status without touching Outputs, so the prior run's
		// outputs survive into the next setup as .Prev. The same invariant must
		// hold across setup retries: a failed run preserves the prior outputs
		// so the next attempt can read .Prev — otherwise scripts that resume
		// from a previous run (claude --resume etc.) lose their handle on retry.
		var prev map[string]any
		if existing, ok := tasks[r.NodeID]; ok && existing != nil {
			prev = existing.Outputs
		}
		deps := dependencyOutputs(r.DependsOn, tasks)
		resolvedInputs, inputErr := RenderInputs(r.Inputs, deps, workflowOutputs(tasks), session, environmentOutputs(tasks))
		if inputErr != nil {
			tasks[r.NodeID] = failedState(r, now, inputErr.Error(), prev, nil)
			wrapped := fmt.Errorf("node %q input: %w", r.NodeID, inputErr)
			obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, nil)
			return wrapped
		}
		if r.InputsSchema != nil {
			if vErr := r.InputsSchema.Validate(toJSONShape(resolvedInputs)); vErr != nil {
				tasks[r.NodeID] = failedState(r, now, vErr.Error(), prev, resolvedInputs)
				wrapped := fmt.Errorf("node %q input schema: %w", r.NodeID, vErr)
				obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, nil)
				return wrapped
			}
		}
		ctx := RenderContext{
			Self:        map[string]any{},
			Prev:        prev,
			Tasks:       deps,
			Inputs:      resolvedInputs,
			Workflow:    workflowOutputs(tasks),
			Environment: environmentOutputs(tasks),
			Session:     session,
		}
		cmdStr, err := render(r.Setup, ctx)
		if err != nil {
			tasks[r.NodeID] = failedState(r, now, err.Error(), prev, resolvedInputs)
			wrapped := fmt.Errorf("task %q setup template: %w", r.NodeID, err)
			obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, nil)
			return wrapped
		}
		outputs := map[string]any{}
		var stderrCaptured []byte
		if strings.TrimSpace(cmdStr) != "" {
			stdout, stderr, runErr := execForNode(goCtx, r.Execution, ee, cmdStr, session.WorktreePath)
			stderrCaptured = stderr
			if runErr != nil {
				tasks[r.NodeID] = failedState(r, now, runErr.Error(), prev, resolvedInputs)
				wrapped := fmt.Errorf("task %q setup: %w", r.NodeID, runErr)
				obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, stderr)
				return wrapped
			}
			var parseErr error
			outputs, parseErr = ParseOutputs(stdout)
			if parseErr != nil {
				tasks[r.NodeID] = failedState(r, now, parseErr.Error(), prev, resolvedInputs)
				wrapped := fmt.Errorf("task %q setup: %w", r.NodeID, parseErr)
				obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, stderr)
				return wrapped
			}
		}
		if r.OutputsSchema != nil {
			if vErr := r.OutputsSchema.Validate(outputs); vErr != nil {
				tasks[r.NodeID] = failedState(r, now, vErr.Error(), prev, resolvedInputs)
				wrapped := fmt.Errorf("task %q setup: outputs schema: %w", r.NodeID, vErr)
				obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, stderrCaptured)
				return wrapped
			}
		}
		tasks[r.NodeID] = &contract.TaskState{
			Scope:   r.Scope,
			TaskID:  taskIDFor(r),
			Status:  contract.TaskStatusProduced,
			Inputs:  resolvedInputs,
			Outputs: outputs,
			Seq:     nextSeq(tasks),
			SetupAt: now,
		}
		obs.OnSuccess(r.Scope, r.NodeID, time.Since(now), stderrCaptured)
	}
	return nil
}

// NextSeq is the exported form of nextSeq: the next instantiation sequence
// number for a tasks map. Callers persisting a dynamic instance re-stamp Seq
// with this under the state lock, so the value reflects the freshly-read map
// rather than the snapshot the setup ran against (atomic read-modify-write).
func NextSeq(tasks map[string]*contract.TaskState) int {
	return nextSeq(tasks)
}

// nextSeq returns the next instantiation sequence number for the tasks map:
// one past the highest Seq currently recorded. Seq is stamped when a task
// reaches "produced" (workflow pseudo-node, static node, or dynamic instance),
// so teardown can reclaim tasks in reverse-instantiation order regardless of
// scope or origin. Legacy state with no Seq (all zero) leaves later assignments
// starting at 1; the teardown path falls back to plan order in that case.
func nextSeq(tasks map[string]*contract.TaskState) int {
	max := 0
	for _, st := range tasks {
		if st != nil && st.Seq > max {
			max = st.Seq
		}
	}
	return max + 1
}

// taskIDFor returns the task id when it differs from the node id;
// otherwise empty so legacy state files (where node id == task id) round-trip
// without an `task_id` field appearing in JSON.
func taskIDFor(r Resolved) string {
	if r.TaskID == "" || r.TaskID == r.NodeID {
		return ""
	}
	return r.TaskID
}

func failedState(r Resolved, now time.Time, errMsg string, prev, inputs map[string]any) *contract.TaskState {
	return &contract.TaskState{
		Scope:    r.Scope,
		TaskID:   taskIDFor(r),
		Status:   contract.TaskStatusFailed,
		Inputs:   inputs,
		Outputs:  prev,
		FailedAt: now,
		Error:    errMsg,
	}
}

// toJSONShape normalizes a map[string]any (with string-typed leaves from
// RenderInputs) so JSON Schema validation sees the same shape it would after
// a JSON round-trip. Current implementation only stores strings, so this is a
// no-op pass-through; kept as a single seam in case node inputs grow non-string
// support.
func toJSONShape(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// RunCleanup executes cleanup commands for the given ordered slice IN REVERSE.
// Each node's cleanup is run with .Self bound to its persisted outputs and
// .Input bound to the inputs persisted at setup time — so cleanup never
// depends on the original CLI invocation.
// Cleanup errors are collected but do not stop the loop — all cleanups attempt.
//
// envExecutor is optional (variadic so every pre-existing call site keeps
// compiling unchanged): when supplied, a node whose resolved Execution is
// "environment" runs through it instead of the host.
func RunCleanup(goCtx context.Context, ordered []Resolved, session SessionVars, tasks map[string]*contract.TaskState, observer Observer, envExecutor ...Executor) error {
	obs := observerOr(observer)
	ee := firstExecutor(envExecutor)
	var firstErr error
	for i := len(ordered) - 1; i >= 0; i-- {
		r := ordered[i]
		now := time.Now()
		state, ok := tasks[r.NodeID]
		if !ok || state == nil {
			// Nothing to clean — task never ran setup successfully.
			obs.OnSkip(r.Scope, r.NodeID, "no setup state")
			continue
		}
		if state.Status == contract.TaskStatusCleaned {
			obs.OnSkip(r.Scope, r.NodeID, "already cleaned")
			continue
		}
		if strings.TrimSpace(r.Cleanup) == "" {
			// No cleanup script means the task ends as soon as we say it
			// does — the entity has been "released" with no work required.
			// Surface this as success rather than skip so the progress UI
			// matches the new status semantics (cleaned ≡ gone).
			state.Status = contract.TaskStatusCleaned
			state.CleanedAt = now
			obs.OnSuccess(r.Scope, r.NodeID, time.Since(now), nil)
			continue
		}
		obs.OnStart(r.Scope, r.NodeID)
		// A dynamic instance's cleanup must see its OWN resource as .ResourceID,
		// the same one its setup bound — not the session's. SessionVars
		// is a value, so copying and overriding is local to this task.
		sess := session
		if state.Resource != "" {
			sess.ResourceID = state.Resource
		}
		ctx := RenderContext{
			Self:        state.Outputs,
			Tasks:       dependencyOutputs(r.DependsOn, tasks),
			Inputs:      state.Inputs,
			Workflow:    workflowOutputs(tasks),
			Environment: environmentOutputs(tasks),
			Session:     sess,
		}
		cmdStr, err := renderCleanup(r.Cleanup, ctx)
		if err != nil {
			state.Status = contract.TaskStatusFailed
			state.Error = err.Error()
			state.FailedAt = now
			wrapped := fmt.Errorf("task %q cleanup template: %w", r.NodeID, err)
			if firstErr == nil {
				firstErr = wrapped
			}
			obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, nil)
			continue
		}
		_, stderr, runErr := execForNode(goCtx, r.Execution, ee, cmdStr, session.WorktreePath)
		if runErr != nil {
			state.Status = contract.TaskStatusFailed
			state.Error = runErr.Error()
			state.FailedAt = now
			wrapped := fmt.Errorf("task %q cleanup: %w", r.NodeID, runErr)
			if firstErr == nil {
				firstErr = wrapped
			}
			obs.OnFailure(r.Scope, r.NodeID, time.Since(now), wrapped, stderr)
			continue
		}
		state.Status = contract.TaskStatusCleaned
		state.CleanedAt = now
		state.Error = ""
		obs.OnSuccess(r.Scope, r.NodeID, time.Since(now), stderr)
	}
	return firstErr
}
