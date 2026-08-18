package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// OutputKeyInteractiveEndpoint is the public output a nesting chain's
// terminal-declaring layer projects into the composed public contract, so
// attach/capture/send reach that layer through the contract rather than
// through nesting-aware lookup.
const OutputKeyInteractiveEndpoint = "interactive_endpoint"

// BindConfig is a nested task's `[bind.*]` tables — the only
// nesting-specific vocabulary besides `inner` and `locals`. Inputs renders
// the inner task's input object, Env adds process environment to the inner
// task's own executions, and Outputs binds the composed task's public
// outputs from inner outputs or this layer's locals.
type BindConfig struct {
	Inputs  map[string]string `toml:"inputs"`
	Env     map[string]string `toml:"env"`
	Outputs map[string]string `toml:"outputs"`
}

// InputBindings / EnvBindings / OutputBindings are nil-safe readers, so a
// layer that declares no `[bind]` table at all needs no guard at each use.
func (b *BindConfig) InputBindings() map[string]string {
	if b == nil {
		return nil
	}
	return b.Inputs
}

func (b *BindConfig) EnvBindings() map[string]string {
	if b == nil {
		return nil
	}
	return b.Env
}

func (b *BindConfig) OutputBindings() map[string]string {
	if b == nil {
		return nil
	}
	return b.Outputs
}

// IsNested reports whether this definition is the outer layer of a nesting
// chain.
func (d TaskDefinition) IsNested() bool {
	return strings.TrimSpace(d.Inner) != ""
}

// ResolvedLocalsSchemaPath joins LocalsSchemaFile with BaseDir.
func (d TaskDefinition) ResolvedLocalsSchemaPath() string {
	return resolveSchemaPath(d.LocalsSchemaFile, d.BaseDir)
}

// envNameRE is the POSIX-portable process environment name charset. A
// `bind.env` key outside it could never be exported to the inner task's
// executions, so it is rejected where it is written rather than swallowed by
// the shell at run time.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// outputBinding is one `[bind.outputs]` entry after classification.
//
// Direct bindings project the inner output's native value and route mutable
// writes to it; computed bindings are rendered strings. The two carry
// different schema obligations, so which one an entry is has to be decided
// statically, from the template body alone.
type outputBinding struct {
	// Key is the public output name this entry defines.
	Key string
	// Direct is true when the whole template body is exactly one
	// `.Inner.outputs.<key>` reference.
	Direct bool
	// InnerKey is the bound inner output for a direct binding.
	InnerKey string
	// InnerRefs lists every inner output the template reads, direct or not.
	InnerRefs []string
}

// classifyOutputBinding decides whether tmpl is a direct inner-output
// binding and collects the sources it reads, rejecting any source that is
// neither an inner public output nor a local.
func classifyOutputBinding(key, tmpl string) (outputBinding, error) {
	b := outputBinding{Key: key}
	t, err := template.New("bind-output").Parse(tmpl)
	if err != nil {
		return b, fmt.Errorf("bind.outputs %q: %w", key, err)
	}
	var refErr error
	seen := map[string]bool{}
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		if n == nil || refErr != nil {
			return
		}
		switch x := n.(type) {
		case *parse.ListNode:
			if x == nil {
				return
			}
			for _, c := range x.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x == nil {
				return
			}
			for _, c := range x.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			for _, a := range x.Args {
				walk(a)
			}
		case *parse.IfNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.FieldNode:
			switch {
			case x.Ident[0] == "Inner":
				if len(x.Ident) < 3 || x.Ident[1] != "outputs" {
					refErr = fmt.Errorf("bind.outputs %q reads %q; an inner reference names one inner public output as `.Inner.outputs.<key>`", key, "."+strings.Join(x.Ident, "."))
					return
				}
				if !seen[x.Ident[2]] {
					seen[x.Ident[2]] = true
					b.InnerRefs = append(b.InnerRefs, x.Ident[2])
				}
			case x.Ident[0] == "Locals":
			default:
				refErr = fmt.Errorf("bind.outputs %q reads %q, which is neither an inner public output nor a local (`.Inner.outputs.<key>` or `.Locals.<key>`)", key, "."+strings.Join(x.Ident, "."))
			}
		case *parse.DotNode:
			refErr = fmt.Errorf("bind.outputs %q reads the whole render context, which is neither an inner public output nor a local", key)
		}
	}
	walk(t.Root)
	if refErr != nil {
		return b, refErr
	}
	sort.Strings(b.InnerRefs)
	if inner, ok := soleInnerOutputRef(t.Root); ok {
		b.Direct = true
		b.InnerKey = inner
	}
	return b, nil
}

// soleInnerOutputRef reports the inner output key when the template body is
// exactly one `.Inner.outputs.<key>` action — the sole shape that projects
// the inner value natively instead of rendering it to a string. Surrounding
// whitespace is tolerated because it is not part of the value the author
// wrote; any other literal text, extra action, function call, or pipeline
// makes the binding computed.
func soleInnerOutputRef(root *parse.ListNode) (string, bool) {
	if root == nil {
		return "", false
	}
	var action *parse.ActionNode
	for _, n := range root.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			if strings.TrimSpace(string(x.Text)) != "" {
				return "", false
			}
		case *parse.ActionNode:
			if action != nil {
				return "", false
			}
			action = x
		default:
			return "", false
		}
	}
	if action == nil || action.Pipe == nil || len(action.Pipe.Cmds) != 1 || len(action.Pipe.Decl) != 0 {
		return "", false
	}
	cmd := action.Pipe.Cmds[0]
	if len(cmd.Args) != 1 {
		return "", false
	}
	field, ok := cmd.Args[0].(*parse.FieldNode)
	if !ok || len(field.Ident) != 3 || field.Ident[0] != "Inner" || field.Ident[1] != "outputs" {
		return "", false
	}
	return field.Ident[2], true
}

// chainLocalRefs returns the `.Work.locals.<key>` references a chain's input
// bindings make. Locals are private to the joint that emitted them, so a
// chain reaching for one instead of for the public output that binds it is a
// wiring mistake worth naming at load time.
func chainLocalRefs(inputs map[string]string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, tmplStr := range inputs {
		t, err := template.New("chain-input").Parse(tmplStr)
		if err != nil {
			return nil, err
		}
		var walk func(n parse.Node)
		walk = func(n parse.Node) {
			switch x := n.(type) {
			case *parse.ListNode:
				if x == nil {
					return
				}
				for _, c := range x.Nodes {
					walk(c)
				}
			case *parse.ActionNode:
				walk(x.Pipe)
			case *parse.PipeNode:
				if x == nil {
					return
				}
				for _, c := range x.Cmds {
					walk(c)
				}
			case *parse.CommandNode:
				for _, a := range x.Args {
					walk(a)
				}
			case *parse.FieldNode:
				if len(x.Ident) >= 3 && x.Ident[0] == "Work" && x.Ident[1] == "locals" && !seen[x.Ident[2]] {
					seen[x.Ident[2]] = true
					out = append(out, x.Ident[2])
				}
			}
		}
		walk(t.Root)
	}
	sort.Strings(out)
	return out, nil
}

// schemaPropertyType returns a property's declared `type` when it is a plain
// string. A union type (array form) or an absent one leaves nothing for the
// binding rules to disagree with, so both read as undeclared.
func schemaPropertyType(props map[string]any, key string) (string, bool) {
	prop, ok := props[key].(map[string]any)
	if !ok {
		return "", false
	}
	t, ok := prop["type"].(string)
	return t, ok
}

// schemaPropertyMutable reports the property's `mutable` annotation.
func schemaPropertyMutable(props map[string]any, key string) bool {
	prop, ok := props[key].(map[string]any)
	if !ok {
		return false
	}
	b, _ := prop["mutable"].(bool)
	return b
}

// SchemaRequiredNames returns the schema document's `required` list, and
// SchemaIsClosed reports whether it sets `additionalProperties = false`.
// Together they are what `bind.inputs` has to satisfy for the inner task:
// every required input bound, and nothing bound that a closed schema rejects.
func SchemaRequiredNames(inline map[string]any, filePath string) ([]string, error) {
	raw, err := loadRawSchema(inline, filePath)
	if err != nil || raw == nil {
		return nil, err
	}
	items, _ := raw["required"].([]any)
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func SchemaIsClosed(inline map[string]any, filePath string) (bool, error) {
	raw, err := loadRawSchema(inline, filePath)
	if err != nil || raw == nil {
		return false, err
	}
	allowed, ok := raw["additionalProperties"].(bool)
	return ok && !allowed, nil
}

// ValidateTaskRequires enforces the `requires` contract: when a task declares
// `requires`, every done_when check leaf must read a required output, and
// every required output must be a property of the task's outputs schema. This
// makes the done_when ↔ observed-output wiring explicit and catches typos at
// compile time. A nil/empty `requires` is unconstrained (opt-in). Each layer
// of a nesting chain answers for its own additions, so this applies per layer.
func ValidateTaskRequires(def TaskDefinition) error {
	if len(def.Requires) == 0 {
		return nil
	}
	required := make(map[string]bool, len(def.Requires))
	for _, r := range def.Requires {
		required[r] = true
	}
	if def.DoneWhen != nil {
		for i, leaf := range def.DoneWhen.All {
			if strings.TrimSpace(leaf.Check) == "" {
				continue
			}
			if !required[leaf.Check] {
				return fmt.Errorf("done_when.all[%d] reads output %q which is not declared in `requires` %v", i, leaf.Check, def.Requires)
			}
		}
	}
	props, err := SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
	if err != nil {
		return fmt.Errorf("outputs schema: %w", err)
	}
	if len(props) > 0 {
		declared := make(map[string]bool, len(props))
		for _, p := range props {
			declared[p] = true
		}
		for _, r := range def.Requires {
			if !declared[r] {
				return fmt.Errorf("`requires` names output %q which the outputs schema does not declare", r)
			}
		}
	}
	return nil
}
