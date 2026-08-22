package configlang

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ShellExecution is the process one shell action runs. Its argv carries the
// action's operands and nothing else, and it adds nothing to the process
// environment, so a bound value is visible neither in the process table nor
// to anything the script starts.
type ShellExecution struct {
	Path     string
	Operands []string
}

const (
	bindingFileName = "bindings.sh"
	scriptFileName  = "script.sh"
	wrapperFileName = "run.sh"

	// reservedBindPrefix is Plecture's own variable namespace. A binding may
	// not claim a name in it: the wrapper below no longer reads any variable,
	// but a wrapper that ever did would let a bound value choose the source
	// that runs, and reserving the namespace keeps that regression from being
	// silent.
	reservedBindPrefix = "PLECT_"
)

var shellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MaterializeShellAction prepares one shell action for execution in dir, a
// private run directory: the author's shell source verbatim, a mode-0600
// file assigning the resolved bindings, and a wrapper that sources the first
// into the second. bound supplies one resolved value per bind key.
//
// Nothing renders a value into the shell source and nothing passes one on
// argv, so a bound value cannot become part of the command that runs.
// Plecture-owned generation escapes each value exactly once, which is what
// makes a value carrying shell syntax arrive as data.
func MaterializeShellAction(dir string, a *Action, bound map[string]string, operands []string) (*ShellExecution, error) {
	if a.Type != actionShell {
		return nil, fmt.Errorf("the binding transport runs a shell action, not %s", a.Type)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory's mode alone, and a run
	// directory holding a binding file must not be group- or world-readable
	// however it came to exist.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}

	assignments, err := bindingAssignments(a, bound)
	if err != nil {
		return nil, err
	}
	bindingPath := filepath.Join(dir, bindingFileName)
	scriptPath := filepath.Join(dir, scriptFileName)
	wrapperPath := filepath.Join(dir, wrapperFileName)

	if err := os.WriteFile(bindingPath, []byte(assignments), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptPath, []byte(a.Script), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(wrapperPath, []byte(wrapperSource(bindingPath, scriptPath)), 0o700); err != nil {
		return nil, err
	}
	return &ShellExecution{Path: wrapperPath, Operands: operands}, nil
}

// wrapperSource sources the bindings into the shell that runs the script,
// rather than exporting them, so a bound value stays inside this shell
// instead of being inherited by every process the script starts. Sourcing
// also preserves the wrapper's positional parameters, which is how a
// terminal verb receives its operand.
//
// Both paths are quoted literals rather than variable expansions: a wrapper
// that read them from the environment would hand a binding that shadowed one
// of those variables — or an ambient variable of the same name — the choice
// of which source gets executed. The wrapper sets no shell option of its
// own, because whatever it set would apply to the author's script too.
func wrapperSource(bindingPath, scriptPath string) string {
	return "#!/usr/bin/env sh\n" +
		". " + singleQuote(bindingPath) + "\n" +
		". " + singleQuote(scriptPath) + "\n"
}

func bindingAssignments(a *Action, bound map[string]string) (string, error) {
	var b strings.Builder
	for _, name := range sortedBindKeys(a.Bind) {
		if !shellName.MatchString(name) {
			return "", fmt.Errorf("bind key %q is not a shell variable name", name)
		}
		if strings.HasPrefix(name, reservedBindPrefix) {
			return "", fmt.Errorf("bind key %q claims a name in Plecture's own %s* namespace", name, reservedBindPrefix)
		}
		value, ok := bound[name]
		if !ok {
			return "", fmt.Errorf("bind key %q has no resolved value", name)
		}
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(singleQuote(value))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// singleQuote wraps a value so the shell reads every byte of it literally.
func singleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
