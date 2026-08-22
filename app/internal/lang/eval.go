package lang

import (
	"fmt"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
)

// Environment holds what a surface's roots contain at one evaluation: a tree
// mirroring the dotted root paths values.md declares, so
// `{ from = "workspace.dir" }` reads the "dir" key of the "workspace" root.
// A root that has nothing to report is absent from the tree rather than
// present and empty, which is what lets `default` and `optional` tell the
// two apart.
type Environment map[string]any

// Eval is Validation's runtime counterpart: where Validation checks a value
// against the roots its surface declares, Eval resolves it against what
// those roots hold now. Bin and Terminal are supplied by the caller because
// an executable's path and an interactive endpoint are facts about this
// machine and this session rather than about the configuration; a nil hook
// makes that capability unavailable rather than resolving to nothing.
type Eval struct {
	Env      Environment
	Bin      func(ref string) (string, error)
	Terminal func(verb string) (string, error)
}

// Execution is the process one action runs. Both variants land here, so a
// caller that only has to start the process does not have to know which
// variant it came from.
type Execution struct {
	Argv  []string
	Stdin []byte
}

// Run resolves either action variant. dir is the private run directory the
// binding transport writes into, and is untouched by an exec action — a
// caller that knows it is running an exec action may pass "".
func (e Eval) Run(dir string, a *Action, operands []string) (*Execution, error) {
	if a.Type == ActionShell {
		return e.Shell(dir, a, operands)
	}
	return e.Exec(a)
}

// Exec resolves one exec action into the process it runs. Argv[0] is the
// resolved executable, so nothing downstream has to know whether the action
// named it through bin or command.
func (e Eval) Exec(a *Action) (*Execution, error) {
	if a.Type != ActionExec {
		return nil, fmt.Errorf("this is an exec evaluation, not %s", a.Type)
	}
	path := a.Command
	if a.Bin != "" {
		resolved, err := e.resolveBin(a.Bin)
		if err != nil {
			return nil, err
		}
		path = resolved
	}
	argv := []string{path}
	for i, arg := range a.Args {
		value, absent, err := e.Argument(arg)
		if err != nil {
			return nil, fmt.Errorf("args[%d]: %w", i, err)
		}
		// An absent element is dropped rather than passed as an empty
		// string, matching the json serializer's own treatment of an absent
		// operand; an author who needs the position held writes `default`.
		if absent {
			continue
		}
		argv = append(argv, value)
	}
	exec := &Execution{Argv: argv}
	if a.Stdin != nil {
		stdin, absent, err := e.Stdin(a.Stdin)
		if err != nil {
			return nil, fmt.Errorf("stdin: %w", err)
		}
		if !absent {
			exec.Stdin = stdin
		}
	}
	return exec, nil
}

// Shell resolves one shell action's bindings and materializes it in dir.
func (e Eval) Shell(dir string, a *Action, operands []string) (*Execution, error) {
	if a.Type != ActionShell {
		return nil, fmt.Errorf("this is a shell evaluation, not %s", a.Type)
	}
	bound := make(map[string]string, len(a.Bind))
	for _, name := range sortedBindKeys(a.Bind) {
		value, absent, err := e.Argument(a.Bind[name])
		if err != nil {
			return nil, fmt.Errorf("bind.%s: %w", name, err)
		}
		// A shell variable is either assigned or not, and an unassigned one
		// reads as empty inside the script — indistinguishable from a
		// declared empty value. A binding therefore has to resolve, and an
		// author who wants emptiness declares `default = ""`.
		if absent {
			return nil, fmt.Errorf("bind.%s resolved to nothing; declare a default", name)
		}
		bound[name] = value
	}
	shell, err := MaterializeShellAction(dir, a, bound, operands)
	if err != nil {
		return nil, err
	}
	return &Execution{Argv: append([]string{shell.Path}, shell.Operands...)}, nil
}

// Argument resolves one value to the single string an argv element or a
// shell binding carries.
func (e Eval) Argument(v *Value) (string, bool, error) {
	if v.Form == FormJSON {
		data, absent, err := e.Stdin(v)
		return string(data), absent, err
	}
	resolved, absent, err := e.Value(v)
	if err != nil || absent {
		return "", absent, err
	}
	s, err := stringify(resolved)
	if err != nil {
		return "", false, err
	}
	return s, false, nil
}

// Stdin resolves the value written to a process's standard input. A json
// operand reaches it as the bytes the serializer produced, without a further
// round trip through a string.
func (e Eval) Stdin(v *Value) ([]byte, bool, error) {
	if v.Form == FormJSON {
		return RenderJSON(v.JSON, func(leaf *Value) (any, bool, error) {
			return e.Value(leaf)
		})
	}
	s, absent, err := e.Argument(v)
	if err != nil || absent {
		return nil, absent, err
	}
	return []byte(s), false, nil
}

// Value resolves one value to its native Go value, preserving the type the
// projection found.
func (e Eval) Value(v *Value) (any, bool, error) {
	switch v.Form {
	case FormLiteral:
		return v.Literal, false, nil
	case FormFrom:
		resolved, found := e.project(v.From)
		switch {
		case found:
			return resolved, false, nil
		case v.HasDefault:
			return v.Default, false, nil
		case v.Optional:
			return nil, true, nil
		default:
			return nil, false, fmt.Errorf("%q resolved to nothing and declares neither default nor optional", v.From)
		}
	case FormExpr:
		resolved, err := e.expr(v.Expr)
		return resolved, false, err
	case FormBin:
		resolved, err := e.resolveBin(v.Bin)
		return resolved, false, err
	case FormTerminal:
		if e.Terminal == nil {
			return nil, false, fmt.Errorf("the terminal capability %q is not available here", v.Terminal)
		}
		resolved, err := e.Terminal(v.Terminal)
		return resolved, false, err
	default:
		data, absent, err := e.Stdin(v)
		return string(data), absent, err
	}
}

func (e Eval) resolveBin(ref string) (string, error) {
	if e.Bin == nil {
		return "", fmt.Errorf("the executable %q cannot be resolved here", ref)
	}
	return e.Bin(ref)
}

// project walks a dotted root path through the environment tree. A path
// running through anything but a table reports absence rather than an error:
// whether the contract declares that field is a load-time question, already
// answered by the time anything is evaluated.
func (e Eval) project(path string) (any, bool) {
	var current any = map[string]any(e.Env)
	for _, segment := range strings.Split(path, ".") {
		tbl, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = tbl[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// expr evaluates a CEL expression against the roots the environment
// actually carries. The variables it declares come from the environment
// rather than from the surface: the surface's roots were already checked at
// load, and an expression naming a root that has nothing to report has to
// fail rather than read as empty.
func (e Eval) expr(src string) (any, error) {
	base, err := profileEnv()
	if err != nil {
		return nil, err
	}
	opts := make([]cel.EnvOption, 0, len(e.Env))
	for name := range e.Env {
		opts = append(opts, cel.Variable(name, cel.DynType))
	}
	env, err := base.Extend(opts...)
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(src)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("expr %q: %s", src, oneLine(issues.Err()))
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("expr %q: %w", src, err)
	}
	out, _, err := program.Eval(map[string]any(e.Env))
	if err != nil {
		return nil, fmt.Errorf("expr %q: %w", src, err)
	}
	return out.Value(), nil
}

// stringify is the one place a native value becomes the single string an
// argv element or a shell binding can carry. A composite has no such form,
// so it is refused rather than silently serialized in a shape no receiver
// agreed to.
func stringify(v any) (string, error) {
	switch value := v.(type) {
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case uint64:
		return strconv.FormatUint(value, 10), nil
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("a value of type %T has no single-string form; serialize it with { json = ... }", v)
	}
}
