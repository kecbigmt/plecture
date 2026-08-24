package task

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// StaticInputIssue is one input-shape violation ValidateInputsStatic found
// without live dispatch data: which input key is at fault, and why.
type StaticInputIssue struct {
	Key     string
	Message string
}

// ValidateInputsStatic checks a node's declared input bindings against its
// own compiled InputsSchema — the same schema and the same Validate call
// RunSetup runs against the resolved inputs at dispatch (task.go's
// toJSONShape+InputsSchema.Validate) — using only what a workflow's config
// already declares, so a mismatch surfaces at verify time instead of only
// once dispatch reaches that node.
//
// An unknown key (`additionalProperties = false`) and a missing required key
// are decidable from the declared key set alone. A type or pattern violation
// is decidable only for a key whose value is known before dispatch: a
// literal, or a `{ from = ..., default = ... }` binding's own default. A
// plain `from`, `expr`, `terminal`, `bin`, or `json` binding's key still
// counts toward `required`/`additionalProperties` — both are presence
// checks — but this function has no value to hold against its type or
// pattern, so it stays silent there and leaves that check to dispatch.
func ValidateInputsStatic(r Resolved) []StaticInputIssue {
	if r.InputsSchema == nil {
		return nil
	}
	shape, known := staticInputShape(r.Inputs)
	err := r.InputsSchema.Validate(shape)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []StaticInputIssue{{Message: err.Error()}}
	}
	issues := collectDecidableIssues(ve, known, nil)
	if len(issues) == 0 {
		return nil
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Key != issues[j].Key {
			return issues[i].Key < issues[j].Key
		}
		return issues[i].Message < issues[j].Message
	})
	return dedupIssues(issues)
}

// staticInputShape builds the best-effort object a node's declared inputs
// resolve to without live dispatch data. known reports which keys carry a
// value this check actually trusts for a type/pattern verdict — a literal,
// or a from-binding's own default; every other key gets a nil placeholder so
// it still counts as present (satisfying required/additionalProperties)
// without pretending to know what dispatch will substitute there.
func staticInputShape(inputs map[string]*lang.Value) (shape map[string]any, known map[string]bool) {
	shape = make(map[string]any, len(inputs))
	known = make(map[string]bool, len(inputs))
	for key, v := range inputs {
		switch {
		case v.Form == lang.FormLiteral:
			shape[key] = v.Literal
			known[key] = true
		case v.Form == lang.FormFrom && v.HasDefault:
			shape[key] = v.Default
			known[key] = true
		default:
			shape[key] = nil
		}
	}
	return shape, known
}

// collectDecidableIssues walks a jsonschema validation error's cause tree,
// keeping only the violations ValidateInputsStatic's doc comment promises:
// required/additionalProperties (decidable from key presence alone,
// regardless of the placeholder standing in for an unresolved value) and any
// other kind at a key staticInputShape actually knows the value of — a type
// or pattern mismatch reported against the nil placeholder would be a false
// positive about a value dispatch hasn't produced yet.
func collectDecidableIssues(e *jsonschema.ValidationError, known map[string]bool, issues []StaticInputIssue) []StaticInputIssue {
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		for _, name := range k.Missing {
			issues = append(issues, StaticInputIssue{Key: name, Message: fmt.Sprintf("missing required input %q", name)})
		}
	case *kind.AdditionalProperties:
		for _, name := range k.Properties {
			issues = append(issues, StaticInputIssue{Key: name, Message: fmt.Sprintf("input %q is not accepted by this task's inputs_schema", name)})
		}
	default:
		if len(e.InstanceLocation) > 0 {
			if key := e.InstanceLocation[0]; known[key] {
				issues = append(issues, StaticInputIssue{Key: key, Message: e.Error()})
			}
		}
	}
	for _, cause := range e.Causes {
		issues = collectDecidableIssues(cause, known, issues)
	}
	return issues
}

func dedupIssues(issues []StaticInputIssue) []StaticInputIssue {
	out := make([]StaticInputIssue, 0, len(issues))
	for i, issue := range issues {
		if i > 0 && issue == issues[i-1] {
			continue
		}
		out = append(out, issue)
	}
	return out
}
