// Package publicoutput derives the record `workflow.outputs.*` reads.
package publicoutput

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Declaration is a workflow's public output contract: the bindings that
// produce the record and the schema the complete record must satisfy.
type Declaration struct {
	Bindings map[string]*lang.Value
	Schema   map[string]any
}

// Declare reads a session's declaration from the workflow revision it froze at
// creation. A session with no revision declares nothing.
func Declare(s *domain.Session) (Declaration, error) {
	// A session predating revisions is backfilled by its own migration; until
	// then it has nothing to derive from and is not a failure.
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

// Evaluate returns the bindings that can be evaluated now. A binding waits
// until every node it reads is produced: a released or failed node keeps its
// last outputs for the next setup's `prev`, and those are not a source.
//
// A complete record that violates the schema is withheld whole rather than
// published half-valid.
func Evaluate(decl Declaration, nodes map[string]*contract.TaskState, sessionInputs map[string]any) (map[string]any, error) {
	bindings := decl.Bindings
	if len(bindings) == 0 {
		return nil, nil
	}
	roots := lang.Roots{
		"nodes":   producedNodeRoots(nodes),
		"session": map[string]any{"inputs": orEmpty(lang.NormalizeOutputs(sessionInputs))},
	}
	eval := lang.Eval{Roots: roots}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(bindings))
	for _, key := range keys {
		binding := bindings[key]
		if !sourcesProduced(key, binding, nodes) {
			continue
		}
		value, absent, err := eval.Value(binding)
		if err != nil || absent {
			continue
		}
		out[key] = value
	}
	if len(out) == len(bindings) && len(decl.Schema) > 0 {
		if err := validate(out, decl.Schema); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Refresh rewrites s.PublicOutputs from the session's frozen workflow
// revision. A session with no revision keeps its record untouched.
func Refresh(s *domain.Session) error {
	if s.WorkflowRevision == "" {
		return nil
	}
	decl, err := Declare(s)
	if err != nil {
		return err
	}
	record, err := Evaluate(decl, s.Nodes, s.Inputs)
	s.PublicOutputs = record
	if err != nil {
		return fmt.Errorf("session %q: %w", s.Name, err)
	}
	return nil
}

func producedNodeRoots(nodes map[string]*contract.TaskState) map[string]any {
	out := make(map[string]any, len(nodes))
	for id, st := range nodes {
		if st == nil || st.Status != contract.TaskStatusProduced {
			continue
		}
		out[id] = map[string]any{"outputs": orEmpty(lang.NormalizeOutputs(st.Outputs))}
	}
	return out
}

func sourcesProduced(key string, binding *lang.Value, nodes map[string]*contract.TaskState) bool {
	for _, id := range lang.NodeReads(map[string]*lang.Value{key: binding}, nil) {
		if st := nodes[id]; st == nil || st.Status != contract.TaskStatusProduced {
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
