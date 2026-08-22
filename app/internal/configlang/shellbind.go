package configlang

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ShellExecution is the process one shell action runs. Its argv carries the
// action's operands and nothing else, and its environment carries only the
// paths of the generated files, so no bound value is visible in the process
// table or inherited by anything the script starts.
type ShellExecution struct {
	Path     string
	Operands []string
	Env      []string
}

const (
	bindingFileName = "bindings.sh"
	scriptFileName  = "script.sh"
	wrapperFileName = "run.sh"

	bindingFileEnv = "PLECT_SHELL_BINDINGS"
	scriptFileEnv  = "PLECT_SHELL_SCRIPT"
)

// shellName is the grammar a bind key has to satisfy to become a shell
// variable name.
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
	if err := os.WriteFile(wrapperPath, []byte(wrapperSource), 0o700); err != nil {
		return nil, err
	}
	return &ShellExecution{
		Path:     wrapperPath,
		Operands: operands,
		Env: []string{
			bindingFileEnv + "=" + bindingPath,
			scriptFileEnv + "=" + scriptPath,
		},
	}, nil
}

// wrapperSource sources the bindings into the shell that runs the script,
// rather than exporting them, so a bound value stays inside this shell
// instead of being inherited by every process the script starts. Sourcing
// also preserves the wrapper's positional parameters, which is how a
// terminal verb receives its operand. The wrapper sets no shell option of
// its own, because whatever it set would apply to the author's script too.
const wrapperSource = `#!/usr/bin/env sh
: "${PLECT_SHELL_BINDINGS:?}" "${PLECT_SHELL_SCRIPT:?}"
. "$PLECT_SHELL_BINDINGS"
unset PLECT_SHELL_BINDINGS
. "$PLECT_SHELL_SCRIPT"
`

func bindingAssignments(a *Action, bound map[string]string) (string, error) {
	var b strings.Builder
	for _, name := range sortedBindKeys(a.Bind) {
		if !shellName.MatchString(name) {
			return "", fmt.Errorf("bind key %q is not a shell variable name", name)
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
