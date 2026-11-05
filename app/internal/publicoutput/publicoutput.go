// Package publicoutput evaluates the record `workflow.outputs.*` reads from a
// session's frozen workflow revision, its nodes, and its inputs.
package publicoutput

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Sources selects which nodes count as a binding's source.
type Sources int

const (
	// ProducedOnly keeps a display, instruction, delivery, or setup from
	// reading a value a released or failed node merely remembers.
	ProducedOnly Sources = iota
	// LastRecorded lets a cleanup release a dependent of a node whose failure
	// is already recorded.
	LastRecorded
)

// Declaration is a workflow's public output contract: the bindings that
// produce the record and the schema the complete record must satisfy.
type Declaration struct {
	Bindings map[string]*lang.Value
	Schema   map[string]any
}

// Declare reads a session's declaration from its frozen workflow revision. A
// session with no revision declares nothing.
func Declare(s *domain.Session) (Declaration, error) {
	if s.WorkflowRevision == "" {
		return Declaration{}, nil
	}
	revision, ok, err := config.RevisionForSession(s)
	if err != nil {
		return Declaration{}, fmt.Errorf("session %q: %w", s.Name, err)
	}
	if !ok {
		return Declaration{}, nil
	}
	return Declaration{Bindings: revision.Workflow.Outputs, Schema: revision.Workflow.OutputsSchema}, nil
}

func Of(s *domain.Session, sources Sources) (map[string]any, error) {
	decl, err := Declare(s)
	if err != nil {
		return nil, err
	}
	return Evaluate(decl, s.Nodes, s.Inputs, sources)
}

// Evaluate returns the bindings whose sources all count. One that still fails
// is unset and named in the returned error beside the rest of the record; one
// that resolves to nothing, such as an omitted optional output, is unset
// silently. A complete record that violates the schema is withheld whole.
func Evaluate(decl Declaration, nodes map[string]*contract.TaskState, sessionInputs map[string]any, sources Sources) (map[string]any, error) {
	bindings := decl.Bindings
	if len(bindings) == 0 {
		return nil, nil
	}
	eval := lang.Eval{Roots: lang.Roots{
		"nodes":   nodeRoots(nodes, sources),
		"session": map[string]any{"inputs": orEmpty(lang.NormalizeOutputs(sessionInputs))},
	}}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(bindings))
	var failed []string
	for _, key := range keys {
		binding := bindings[key]
		if !sourcesCount(key, binding, nodes, sources) {
			continue
		}
		value, absent, err := eval.Value(binding)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%q: %v", key, err))
			continue
		}
		if absent {
			continue
		}
		out[key] = value
	}
	var problem error
	if len(failed) > 0 {
		problem = fmt.Errorf("public outputs not evaluable although their sources exist: %s", strings.Join(failed, "; "))
	}
	if len(out) == len(bindings) && len(decl.Schema) > 0 {
		if err := validate(out, decl.Schema); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, problem
	}
	return out, problem
}

func counts(st *contract.TaskState, sources Sources) bool {
	if st == nil {
		return false
	}
	return sources == LastRecorded || st.Status == contract.TaskStatusProduced
}

func nodeRoots(nodes map[string]*contract.TaskState, sources Sources) map[string]any {
	out := make(map[string]any, len(nodes))
	for id, st := range nodes {
		if counts(st, sources) {
			out[id] = map[string]any{"outputs": orEmpty(lang.NormalizeOutputs(st.Outputs))}
		}
	}
	return out
}

func sourcesCount(key string, binding *lang.Value, nodes map[string]*contract.TaskState, sources Sources) bool {
	for _, id := range lang.NodeReads(map[string]*lang.Value{key: binding}, nil) {
		if !counts(nodes[id], sources) {
			return false
		}
	}
	return true
}

func validate(record, schema map[string]any) error {
	compiled, err := lang.CompileInlineSchema(schema, "plect:workflow:outputs")
	if err != nil {
		return fmt.Errorf("outputs_schema: %w", err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("outputs_schema: %w", err)
	}
	var shaped any
	if err := json.Unmarshal(raw, &shaped); err != nil {
		return fmt.Errorf("outputs_schema: %w", err)
	}
	if err := compiled.Validate(shaped); err != nil {
		return fmt.Errorf("public outputs violate outputs_schema: %w", err)
	}
	return nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
