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

// OutputBinding is one `[bind.outputs]` entry after classification.
//
// Direct bindings project the inner output's native value and route mutable
// writes to it; computed bindings are rendered strings. The two carry
// different schema obligations and different runtime behavior, so which one
// an entry is has to be decided statically, from the template body alone.
type OutputBinding struct {
	// Key is the public output name this entry defines.
	Key string
	// Template is the entry's declared template body, which the projection
	// renders for a computed binding.
	Template string
	// Direct is true when the whole template body is exactly one
	// `.Inner.outputs.<key>` reference.
	Direct bool
	// InnerKey is the bound inner output for a direct binding.
	InnerKey string
	// InnerRefs lists every inner output the template reads, direct or not.
	InnerRefs []string
}

// walkTemplateNodes visits every node of a parsed template, descending into
// the branches of if/range/with. Every static reference scan in this package
// shares it: a rule that stops at the top level would let the same reference
// through unexamined merely for sitting inside a conditional.
func walkTemplateNodes(n parse.Node, visit func(parse.Node)) {
	if n == nil {
		return
	}
	visit(n)
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return
		}
		for _, c := range x.Nodes {
			walkTemplateNodes(c, visit)
		}
	case *parse.ActionNode:
		walkTemplateNodes(x.Pipe, visit)
	case *parse.PipeNode:
		if x == nil {
			return
		}
		for _, c := range x.Cmds {
			walkTemplateNodes(c, visit)
		}
	case *parse.CommandNode:
		for _, a := range x.Args {
			walkTemplateNodes(a, visit)
		}
	case *parse.IfNode:
		walkBranch(x.BranchNode, visit)
	case *parse.RangeNode:
		walkBranch(x.BranchNode, visit)
	case *parse.WithNode:
		walkBranch(x.BranchNode, visit)
	}
}

func walkBranch(b parse.BranchNode, visit func(parse.Node)) {
	walkTemplateNodes(b.Pipe, visit)
	walkTemplateNodes(b.List, visit)
	walkTemplateNodes(b.ElseList, visit)
}

// TemplateFieldRefs returns every field reference a template makes, as the
// dotted identifier chain of each (`.Work.outputs.pid` yields
// ["Work","outputs","pid"]), including references inside if/range/with
// branches.
func TemplateFieldRefs(tmplStr string) ([][]string, error) {
	t, err := template.New("ref-scan").Parse(tmplStr)
	if err != nil {
		return nil, err
	}
	var out [][]string
	walkTemplateNodes(t.Root, func(n parse.Node) {
		if f, ok := n.(*parse.FieldNode); ok {
			out = append(out, f.Ident)
		}
	})
	return out, nil
}

// ClassifyOutputBinding decides whether tmpl is a direct inner-output
// binding and collects the sources it reads, rejecting any source that is
// neither an inner public output nor a local.
func ClassifyOutputBinding(key, tmpl string) (OutputBinding, error) {
	b := OutputBinding{Key: key, Template: tmpl}
	t, err := template.New("bind-output").Parse(tmpl)
	if err != nil {
		return b, fmt.Errorf("bind.outputs %q: %w", key, err)
	}
	var refErr error
	seen := map[string]bool{}
	walkTemplateNodes(t.Root, func(n parse.Node) {
		if refErr != nil {
			return
		}
		switch x := n.(type) {
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
	})
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
		refs, err := TemplateFieldRefs(tmplStr)
		if err != nil {
			return nil, err
		}
		for _, ident := range refs {
			if len(ident) >= 3 && ident[0] == "Work" && ident[1] == "locals" && !seen[ident[2]] {
				seen[ident[2]] = true
				out = append(out, ident[2])
			}
		}
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

// ClassifiedOutputBindings returns this layer's `[bind.outputs]` entries in
// a stable key order, each classified as direct or computed. Load-time
// validation and the runtime projection read the same classification from
// here rather than each deciding it again.
func (d TaskDefinition) ClassifiedOutputBindings() ([]OutputBinding, error) {
	bound := d.Bind.OutputBindings()
	keys := make([]string, 0, len(bound))
	for k := range bound {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]OutputBinding, 0, len(keys))
	for _, k := range keys {
		b, err := ClassifyOutputBinding(k, bound[k])
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
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
