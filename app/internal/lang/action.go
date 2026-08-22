package lang

import (
	"fmt"
	"strings"
)

// Action is one lifecycle execution, in either of the language's two
// variants. Exactly one variant's fields are populated, named by Type.
type Action struct {
	Type string

	Bin     string
	Command string
	Args    []*Value
	Stdin   *Value

	Script string
	Bind   map[string]*Value
}

// The two action variants. A runtime consumer reads Type to decide whether
// it has to prepare the binding transport's private run directory, which an
// exec action needs no part of.
const (
	ActionExec  = "exec"
	ActionShell = "shell"
)

var (
	execOnlyFields  = []string{"bin", "command", "args", "stdin"}
	shellOnlyFields = []string{"script", "bind"}
)

// ParseAction reads one action. It checks the variant before anything else,
// so an action naming a type outside the vocabulary is reported as that
// rather than as a field belonging to the variant it happens to resemble.
func ParseAction(raw any, pos Position) (*Action, error) {
	tbl, ok := raw.(map[string]any)
	if !ok {
		return nil, newDiag(CodeFieldType, LayerStructural, pos, "an action is a table")
	}
	typeVal, ok := tbl["type"]
	if !ok {
		return nil, newDiag(CodeFieldRequired, LayerStructural, childPos(pos, "type"),
			"an action declares its type")
	}
	kind, _ := typeVal.(string)
	switch kind {
	case ActionExec:
		return parseExecAction(tbl, pos)
	case ActionShell:
		return parseShellAction(tbl, pos)
	default:
		return nil, newDiag(CodeActionTypeUnknown, LayerStructural, childPos(pos, "type"),
			fmt.Sprintf("an action's type is exec or shell, not %v", typeVal))
	}
}

func parseExecAction(tbl map[string]any, pos Position) (*Action, error) {
	if err := rejectVariantFields(tbl, pos, ActionExec, shellOnlyFields); err != nil {
		return nil, err
	}
	if err := rejectUnknownFields(tbl, pos, "type", "bin", "command", "args", "stdin"); err != nil {
		return nil, err
	}
	a := &Action{Type: ActionExec}
	_, hasBin := tbl["bin"]
	command, hasCommand := tbl["command"]
	if hasBin == hasCommand {
		return nil, newDiag(CodeActionBinAndCommand, LayerStructural, pos,
			"an exec action names its executable exactly once, through bin or command")
	}
	if hasBin {
		bin, err := tagString(tbl, "bin", childPos(pos, "bin"))
		if err != nil {
			return nil, err
		}
		a.Bin = bin
	} else {
		name, ok := command.(string)
		if !ok {
			return nil, newDiag(CodeRefDynamic, LayerStructural, childPos(pos, "command"),
				"an exec action's command is a literal OS command name or path, never a computed value")
		}
		a.Command = name
	}
	if argsVal, ok := tbl["args"]; ok {
		args, ok := argsVal.([]any)
		if !ok {
			return nil, newDiag(CodeFieldType, LayerStructural, childPos(pos, "args"), "args is an array")
		}
		for i, raw := range args {
			v, err := ParseValue(raw, ClassBinding, childPos(childPos(pos, "args"), fmt.Sprintf("[%d]", i)))
			if err != nil {
				return nil, err
			}
			a.Args = append(a.Args, v)
		}
	}
	if stdinVal, ok := tbl["stdin"]; ok {
		v, err := ParseValue(stdinVal, ClassBinding, childPos(pos, "stdin"))
		if err != nil {
			return nil, err
		}
		a.Stdin = v
	}
	return a, nil
}

func parseShellAction(tbl map[string]any, pos Position) (*Action, error) {
	if err := rejectVariantFields(tbl, pos, ActionShell, execOnlyFields); err != nil {
		return nil, err
	}
	if err := rejectUnknownFields(tbl, pos, "type", "script", "bind"); err != nil {
		return nil, err
	}
	a := &Action{Type: ActionShell}
	if _, ok := tbl["script"]; !ok {
		return nil, newDiag(CodeFieldRequired, LayerStructural, childPos(pos, "script"),
			"a shell action declares its script")
	}
	script, err := tagString(tbl, "script", childPos(pos, "script"))
	if err != nil {
		return nil, err
	}
	if strings.Contains(script, "{{") {
		return nil, newDiag(CodeShellInterpolation, LayerStructural, childPos(pos, "script"),
			"shell source is literal; an action declares the values it needs in its bind table instead")
	}
	a.Script = script
	if bindVal, ok := tbl["bind"]; ok {
		bind, ok := bindVal.(map[string]any)
		if !ok {
			return nil, newDiag(CodeFieldType, LayerStructural, childPos(pos, "bind"), "bind is a table")
		}
		a.Bind = make(map[string]*Value, len(bind))
		for name, raw := range bind {
			v, err := ParseValue(raw, ClassBinding, childPos(childPos(pos, "bind"), name))
			if err != nil {
				return nil, err
			}
			a.Bind[name] = v
		}
	}
	return a, nil
}

func rejectVariantFields(tbl map[string]any, pos Position, kind string, foreign []string) error {
	for _, field := range foreign {
		if _, ok := tbl[field]; ok {
			return newDiag(CodeActionVariant, LayerStructural, childPos(pos, field),
				fmt.Sprintf("%s belongs to the other action variant, not to %s", field, kind))
		}
	}
	return nil
}

func rejectUnknownFields(tbl map[string]any, pos Position, allowed ...string) error {
	ok := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		ok[field] = true
	}
	for field := range tbl {
		if !ok[field] {
			return newDiag(CodeFieldUnknown, LayerStructural, childPos(pos, field),
				fmt.Sprintf("%q is not part of this surface", field))
		}
	}
	return nil
}

// sortedBindKeys orders a bind table's keys, so a walk over an action's
// values reports the same diagnostic on every run and a generated binding
// file has stable contents.
func sortedBindKeys(bind map[string]*Value) []string {
	keys := make(map[string]any, len(bind))
	for k := range bind {
		keys[k] = nil
	}
	return sortedKeys(keys)
}

// values returns every value this action carries, so one walk can validate
// argv, stdin, and bindings alike.
func (a *Action) values() []*Value {
	out := make([]*Value, 0, len(a.Args)+len(a.Bind)+1)
	out = append(out, a.Args...)
	if a.Stdin != nil {
		out = append(out, a.Stdin)
	}
	for _, name := range sortedBindKeys(a.Bind) {
		out = append(out, a.Bind[name])
	}
	return out
}
