package task

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

type StaticInputIssue struct {
	Key     string
	Message string
}

// ValidateInputsStatic checks a node's declared input bindings against its
// own compiled InputsSchema, the same schema RunSetup validates the resolved
// inputs against at dispatch. A binding whose value is only known at
// dispatch (anything but a literal or a from-binding's own default) can't be
// type-checked here, so it counts toward required/additionalProperties only.
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
