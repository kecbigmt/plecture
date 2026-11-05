package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/publicoutput"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type RebaseClass string

const (
	RebaseCompatible RebaseClass = "compatible"
	RebaseAutomatic  RebaseClass = "automatic"
	RebaseRestart    RebaseClass = "restart"
	RebaseRecreate   RebaseClass = "recreate"
)

type RebaseClassification struct {
	Class         RebaseClass `json:"class"`
	RecreateNodes []string    `json:"recreate_nodes,omitempty"`
	Reasons       []string    `json:"reasons,omitempty"`
}

type RebaseParams struct {
	Identifier  string
	DryRun      bool
	WithRestart bool
	Observer    task.Observer
	// InvocationDir is the caller's working directory, whose ancestors'
	// `.plect/` overlays contribute the revision being rebased onto.
	InvocationDir string
}

type RebaseResult struct {
	SessionName string `json:"session_name"`
	From        string `json:"from"`
	To          string `json:"to"`
	RebaseClassification
}

func WorkflowRebase(cfg *config.Config, store *state.Store, params RebaseParams) (*RebaseResult, error) {
	name, err := resolveSessionName(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	var result *RebaseResult
	err = withLifecycleLock(store, name, func() error {
		var rebaseErr error
		result, rebaseErr = workflowRebaseUnlocked(cfg, store, name, params)
		return rebaseErr
	})
	return result, err
}

func workflowRebaseUnlocked(cfg *config.Config, store *state.Store, name string, params RebaseParams) (*RebaseResult, error) {
	session, err := store.GetE(name)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("session %q not found", name)
	}
	if session.Workflow == "" {
		return nil, fmt.Errorf("session %q has no workflow", name)
	}
	if session.Status == contract.SessionStatusDestroyed {
		return nil, fmt.Errorf("session %q is destroyed", name)
	}
	old, _, err := config.RevisionForSession(session)
	if err != nil {
		return nil, err
	}
	next, err := cfg.CaptureWorkflowRevision(params.InvocationDir, session.Workflow)
	if err != nil {
		return nil, err
	}
	body, digest, err := next.CanonicalBody()
	if err != nil {
		return nil, err
	}
	next, err = config.DecodeWorkflowRevision(body, digest)
	if err != nil {
		return nil, err
	}
	classification, err := ClassifyWorkflowRebase(old, next, session)
	if err != nil {
		return nil, err
	}
	result := &RebaseResult{SessionName: name, From: session.WorkflowRevision, To: digest, RebaseClassification: classification}
	if params.DryRun {
		return result, nil
	}
	logResult := func(outcome string) error {
		_, _, _, logErr := eventlog.NewStore(store.Dir()).Append(event.Event{SessionName: name, Type: "plect.session.rebase", Source: event.SourcePlect, Direction: event.Internal, Summary: "workflow rebase " + outcome, Metadata: map[string]string{
			"from": result.From, "to": result.To, "class": string(result.Class), "result": outcome, "reasons": strings.Join(result.Reasons, "; "),
		}})
		return logErr
	}
	if classification.Class == RebaseRecreate {
		_ = logResult("rejected")
		return result, fmt.Errorf("workflow rebase requires recreation: %s", strings.Join(classification.Reasons, "; "))
	}
	if classification.Class == RebaseRestart && !params.WithRestart {
		_ = logResult("rejected")
		return result, fmt.Errorf("workflow rebase requires down/up; pass --with-restart: %s", strings.Join(classification.Reasons, "; "))
	}
	if err := checkSessionGuard(cfg, name); err != nil {
		return result, err
	}
	if err := checkLifecycleRelationGuard(store, name, "workflow rebase"); err != nil {
		return result, err
	}
	if classification.Class == RebaseRestart {
		if _, err := downUnlocked(cfg, store, DownParams{Identifier: name, Observer: params.Observer, preserveStatus: true}); err != nil {
			_ = logResult("failed")
			return result, err
		}
		session, err = store.GetE(name)
		if err != nil {
			_ = logResult("failed")
			return result, err
		}
		if err := cleanupRebasedNodes(cfg, store, session, old, next, RebaseClassification{}, params.Observer); err != nil {
			_ = logResult("failed")
			return result, err
		}
	} else if classification.Class == RebaseAutomatic {
		if err := cleanupRebasedNodes(cfg, store, session, old, next, classification, params.Observer); err != nil {
			_ = logResult("failed")
			return result, err
		}
	}
	if err := store.Update(name, func(current *domain.Session) error {
		current.WorkflowRevision = digest
		current.WorkflowRevisionBody = string(body)
		current.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		_ = logResult("failed")
		return result, err
	}
	if session.Status == contract.SessionStatusUp {
		if _, err := upUnlocked(cfg, store, UpParams{Identifier: name, Observer: params.Observer}); err != nil {
			_ = logResult("failed")
			return result, err
		}
	}
	if err := logResult("succeeded"); err != nil {
		return result, err
	}
	return result, nil
}

func cleanupRebasedNodes(cfg *config.Config, store *state.Store, session *domain.Session, old, next config.WorkflowRevision, classification RebaseClassification, observer task.Observer) error {
	plan, err := task.CompileWorkflow(old.Workflow, old.Effects)
	if err != nil {
		return err
	}
	nextIDs := make(map[string]bool)
	for _, node := range next.Workflow.Nodes {
		nextIDs[node.ID] = true
	}
	selected := make(map[string]bool)
	for _, id := range classification.RecreateNodes {
		if !nextIDs[id] || session.Nodes[id] != nil {
			selected[id] = true
		}
	}
	for _, node := range old.Workflow.Nodes {
		if !nextIDs[node.ID] {
			selected[node.ID] = true
		}
	}
	ordered := plan.UpOrder()
	for changed := true; changed; {
		changed = false
		for _, node := range ordered {
			for _, dep := range node.DependsOn {
				if selected[dep] && !selected[node.NodeID] {
					selected[node.NodeID], changed = true, true
				}
			}
		}
	}
	var cleanup []task.Resolved
	for i := len(ordered) - 1; i >= 0; i-- {
		if selected[ordered[i].NodeID] && session.Nodes[ordered[i].NodeID] != nil {
			cleanup = append(cleanup, ordered[i])
		}
	}
	if len(cleanup) == 0 {
		return nil
	}
	observer = withNodeResultRecording(store, session.Name, observer)
	if err := runTaskCleanup(context.Background(), cfg, store, cleanup, sessionVars(cfg, session, plan), session, observer); err != nil {
		return err
	}
	session.UpdatedAt = time.Now()
	return persistStaleWorkflowCleanup(store, session.Name, session, cleanup)
}

// A node's recorded setup directory is part of its execution contract, so
// changing where it runs is a change of the node itself.
func sameExecutionDirectory(old, next task.Resolved) bool {
	return old.Prep == next.Prep && reflect.DeepEqual(old.Workdir, next.Workdir)
}

func ClassifyWorkflowRebase(old, next config.WorkflowRevision, session *domain.Session) (RebaseClassification, error) {
	result := RebaseClassification{Class: RebaseCompatible}
	if session == nil {
		return result, fmt.Errorf("session is nil")
	}
	oldPlan, err := task.CompileWorkflow(old.Workflow, old.Effects)
	if err != nil {
		return result, fmt.Errorf("old workflow: %w", err)
	}
	nextPlan, err := task.CompileWorkflow(next.Workflow, next.Effects)
	if err != nil {
		return result, fmt.Errorf("new workflow: %w", err)
	}
	if old.Workflow.Resource != next.Workflow.Resource {
		result.raise(RebaseRecreate, "entry resource changed; destroy and recreate the session")
	}
	oldNodes := make(map[string]config.WorkflowNode)
	for _, node := range old.Workflow.Nodes {
		oldNodes[node.ID] = node
	}
	nextNodes := make(map[string]config.WorkflowNode)
	for _, node := range next.Workflow.Nodes {
		nextNodes[node.ID] = node
	}
	nextResolved := make(map[string]task.Resolved)
	for _, node := range nextPlan.UpOrder() {
		nextResolved[node.NodeID] = node
	}
	for _, node := range oldPlan.UpOrder() {
		current, exists := nextNodes[node.NodeID]
		if !exists {
			result.raise(RebaseAutomatic, fmt.Sprintf("node %q removed; its saved execution will be cleaned", node.NodeID))
			continue
		}
		oldDef, nextDef := old.Effects[oldNodes[node.NodeID].Uses], next.Effects[current.Uses]
		sameNode := equalRevisionPart(oldNodes[node.NodeID], current) && !readsChangedPublicOutput(current, old.Workflow.Outputs, next.Workflow.Outputs) && sameExecutionDirectory(node, nextResolved[node.NodeID])
		if sameNode && equalRevisionPart(oldDef, nextDef) {
			continue
		}
		if sameNode && onlyAliveChanged(oldDef, nextDef) {
			result.raise(RebaseAutomatic, fmt.Sprintf("alive probe changed for node %q", node.NodeID))
			continue
		}
		if node.Scope != nextResolved[node.NodeID].Scope || node.Scope == config.TaskScopeSession {
			result.raise(RebaseRecreate, fmt.Sprintf("session-scoped node %q changed; destroy and recreate the session", node.NodeID))
			continue
		}
		result.raise(RebaseRestart, fmt.Sprintf("run-scoped node %q changed; pass --with-restart", node.NodeID))
		result.RecreateNodes = append(result.RecreateNodes, node.NodeID)
	}
	for _, node := range nextPlan.UpOrder() {
		if _, exists := oldNodes[node.NodeID]; !exists {
			result.raise(RebaseAutomatic, fmt.Sprintf("node %q added", node.NodeID))
			result.RecreateNodes = append(result.RecreateNodes, node.NodeID)
		}
	}
	oldRun, nextRun, retainedRun := 0, 0, 0
	for _, node := range oldPlan.Run {
		oldRun++
		if newer, ok := nextResolved[node.NodeID]; ok && newer.Scope == config.TaskScopeRun {
			retainedRun++
		}
	}
	for range nextPlan.Run {
		nextRun++
	}
	if oldRun > 0 && nextRun > 0 && retainedRun == 0 {
		result.raise(RebaseRestart, "all run-scoped nodes were replaced; pass --with-restart")
	}
	for _, key := range sortedTaskKeys(session.Tasks) {
		st := session.Tasks[key]
		// Cleanup acts on every instance that is not cleaned, so a failed
		// instance still depends on the definition its setup ran under.
		if st == nil || st.Status == contract.TaskStatusCleaned {
			continue
		}
		address := instanceDefinitionAddress(key, st, true, nil)
		oldDef, wasEffect := old.Effects[address]
		if !wasEffect {
			if oldDoc, wasTask := old.Tasks[address]; wasTask {
				if nextDoc, exists := next.Tasks[address]; !exists {
					result.raise(RebaseRecreate, fmt.Sprintf("dynamic task %q definition %q removed; restore it or destroy and recreate the session", key, address))
				} else if !equalRevisionPart(withoutDocumentPresentation(oldDoc), withoutDocumentPresentation(nextDoc)) {
					result.raise(RebaseRecreate, fmt.Sprintf("session-scoped dynamic task %q changed; destroy and recreate the session", key))
				}
				continue
			}
			result.raise(RebaseRecreate, fmt.Sprintf("dynamic instance %q has no saved definition %q; destroy and recreate the session", key, address))
			continue
		}
		nextDef, exists := next.Effects[address]
		if !exists {
			result.raise(RebaseRecreate, fmt.Sprintf("dynamic instance %q definition %q removed; restore it or destroy and recreate the session", key, address))
		} else if equalRevisionPart(oldDef, nextDef) {
			continue
		} else if onlyAliveChanged(oldDef, nextDef) {
			result.raise(RebaseAutomatic, fmt.Sprintf("alive probe changed for dynamic instance %q", key))
		} else if st.Scope != nextDef.EffectiveScope() || st.Scope == config.TaskScopeSession {
			result.raise(RebaseRecreate, fmt.Sprintf("session-scoped dynamic instance %q changed; destroy and recreate the session", key))
		} else {
			result.raise(RebaseRestart, fmt.Sprintf("run-scoped dynamic instance %q changed; pass --with-restart", key))
		}
	}
	deferred := make(map[string]bool)
	for _, id := range result.RecreateNodes {
		deferred[id] = true
	}
	if result.Class == RebaseRestart {
		for _, node := range nextPlan.Run {
			deferred[node.NodeID] = true
		}
	}
	for _, node := range nextPlan.UpOrder() {
		st := session.Nodes[node.NodeID]
		if st == nil || st.Status != contract.TaskStatusProduced {
			deferred[node.NodeID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, node := range nextPlan.UpOrder() {
			for _, dep := range node.DependsOn {
				if deferred[dep] && !deferred[node.NodeID] {
					deferred[node.NodeID], changed = true, true
				}
			}
		}
	}
	outputs := make(map[string]map[string]any)
	retained := make(map[string]*contract.TaskState)
	for id, state := range session.Nodes {
		if state != nil && state.Status == contract.TaskStatusProduced && !deferred[id] {
			outputs[id] = state.Outputs
			retained[id] = state
		}
	}
	// The public outputs the new declaration would hold once only the
	// retained nodes remain, so an input reading one resolves exactly as it
	// would after the rebase rather than against the previous declaration.
	publicOutputs := publicoutput.Declaration{Bindings: next.Workflow.Outputs, Schema: next.Workflow.OutputsSchema}
	wfOutputs, _ := publicoutput.Evaluate(publicOutputs, retained, session.Inputs, publicoutput.ProducedOnly)
	vars := task.SessionVars{Name: session.Name, ResourceID: session.ResourceID, ParentSession: session.ParentSession, Inputs: session.Inputs}
	for _, node := range nextPlan.UpOrder() {
		if err := validateRebaseInputs(node.Inputs, outputs, wfOutputs, next.Workflow.Outputs, vars, deferred); err != nil {
			result.raise(RebaseRecreate, fmt.Sprintf("node %q inputs cannot resolve from saved outputs: %v; add a default or destroy and recreate the session", node.NodeID, err))
		}
		if state := session.Nodes[node.NodeID]; state != nil && state.Status == contract.TaskStatusProduced && !deferred[node.NodeID] {
			if len(node.Layers) == 0 {
				if err := task.ValidateAliveBindings(node.Health.AliveProbe(), state.Outputs, state.Inputs, vars); err != nil {
					result.raise(RebaseRecreate, fmt.Sprintf("node %q alive binding cannot resolve from saved outputs: %v; add a default or destroy and recreate the session", node.NodeID, err))
				}
			} else if len(state.Layers) != len(node.Layers) {
				result.raise(RebaseRecreate, fmt.Sprintf("node %q nesting records do not match the new declaration; destroy and recreate the session", node.NodeID))
			} else if views, err := task.ProjectLayerOutputs(node.Layers, state.Layers, vars); err != nil {
				result.raise(RebaseRecreate, fmt.Sprintf("node %q layer outputs cannot resolve: %v; destroy and recreate the session", node.NodeID, err))
			} else {
				for i, layer := range node.Layers {
					if err := task.ValidateAliveBindings(layer.Health.AliveProbe(), views[i], state.Layers[i].Inputs, vars); err != nil {
						result.raise(RebaseRecreate, fmt.Sprintf("node %q layer %q alive binding cannot resolve: %v; add a default or destroy and recreate the session", node.NodeID, layer.EffectID, err))
					}
				}
			}
		}
	}
	for _, channel := range next.Workflow.Event.Channel {
		if err := validateRebaseInputs(channel.Inputs, outputs, wfOutputs, next.Workflow.Outputs, vars, deferred); err != nil {
			result.raise(RebaseRecreate, fmt.Sprintf("event channel %q inputs cannot resolve from saved outputs: %v; add a default or destroy and recreate the session", channel.Name, err))
		}
	}
	if result.Class == RebaseRecreate {
		result.RecreateNodes = nil
	} else {
		if result.Class == RebaseRestart {
			for _, node := range nextPlan.Run {
				result.RecreateNodes = append(result.RecreateNodes, node.NodeID)
			}
		} else if result.Class == RebaseAutomatic {
			selected := make(map[string]bool)
			for _, id := range result.RecreateNodes {
				selected[id] = true
			}
			for changed := true; changed; {
				changed = false
				for _, node := range nextPlan.UpOrder() {
					for _, dep := range node.DependsOn {
						if selected[dep] && !selected[node.NodeID] {
							selected[node.NodeID], changed = true, true
						}
					}
				}
			}
			for id := range selected {
				result.RecreateNodes = append(result.RecreateNodes, id)
			}
		}
		unique := make(map[string]bool)
		nodes := result.RecreateNodes[:0]
		for _, id := range result.RecreateNodes {
			if !unique[id] {
				nodes = append(nodes, id)
				unique[id] = true
			}
		}
		result.RecreateNodes = nodes
		sort.Strings(result.RecreateNodes)
		if workdirReleased(old, next, result.RecreateNodes) && liveDynamicInstanceInWorkdir(session) {
			result.raise(RebaseRecreate, "the workdir's producer would be released while a dynamic instance still runs in it; clean the instance up first or destroy and recreate the session")
			result.RecreateNodes = nil
		}
	}
	return result, nil
}

func workdirReleased(old, next config.WorkflowRevision, recreate []string) bool {
	if old.Workflow.Workdir == nil {
		return false
	}
	producer := old.Workflow.Workdir.Node
	if slices.Contains(recreate, producer) {
		return true
	}
	for _, node := range next.Workflow.Nodes {
		if node.ID == producer {
			return false
		}
	}
	return true
}

func liveDynamicInstanceInWorkdir(session *domain.Session) bool {
	for _, st := range session.Tasks {
		if st != nil && st.Status != contract.TaskStatusCleaned && st.Directory != "" {
			return true
		}
	}
	return false
}

func validateRebaseInputs(inputs map[string]*lang.Value, outputs map[string]map[string]any, wfOutputs map[string]any, publicBindings map[string]*lang.Value, vars task.SessionVars, deferred map[string]bool) error {
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, leaf := range lang.ValueLeaves(inputs[key]) {
			readsDeferred := false
			for _, id := range lang.NodeReads(map[string]*lang.Value{key: leaf}, publicBindings) {
				if deferred[id] {
					readsDeferred = true
					break
				}
			}
			if readsDeferred {
				continue
			}
			if _, err := task.ResolveNodeInputs(map[string]*lang.Value{key: leaf}, outputs, wfOutputs, vars); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *RebaseClassification) raise(class RebaseClass, reason string) {
	priority := map[RebaseClass]int{RebaseCompatible: 0, RebaseAutomatic: 1, RebaseRestart: 2, RebaseRecreate: 3}
	if priority[class] > priority[r.Class] {
		r.Class = class
	}
	r.Reasons = append(r.Reasons, reason)
}

// readsChangedPublicOutput reports whether node wires in a public output whose
// binding differs between the two declarations: the node's own text is
// unchanged, but the value it was set up with is not.
func readsChangedPublicOutput(node config.WorkflowNode, old, next map[string]*lang.Value) bool {
	for _, key := range lang.PublicOutputReads(node.Inputs) {
		if !equalRevisionPart(old[key], next[key]) {
			return true
		}
	}
	return false
}

func equalRevisionPart(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

func onlyAliveChanged(a, b config.TaskDefinition) bool {
	if equalRevisionPart(a, b) {
		return false
	}
	return equalRevisionPart(withoutAlive(a), withoutAlive(b))
}

func withoutDocumentPresentation(doc config.TaskDocument) config.TaskDocument {
	doc.Description = ""
	doc.Instruction = ""
	if len(doc.ExtendsLayers) > 0 {
		layers := make([]config.TaskDocument, len(doc.ExtendsLayers))
		for i, layer := range doc.ExtendsLayers {
			layers[i] = withoutDocumentPresentation(layer)
		}
		doc.ExtendsLayers = layers
	}
	return doc
}

func withoutAlive(def config.TaskDefinition) config.TaskDefinition {
	if def.Health == nil {
		def.Health = &config.HealthConfig{}
	} else {
		h := *def.Health
		h.Alive = nil
		def.Health = &h
	}
	if len(def.InnerChain) > 0 {
		chain := make([]config.TaskDefinition, len(def.InnerChain))
		for i, inner := range def.InnerChain {
			chain[i] = withoutAlive(inner)
		}
		def.InnerChain = chain
	}
	return def
}
