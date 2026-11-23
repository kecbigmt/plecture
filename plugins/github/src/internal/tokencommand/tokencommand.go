package tokencommand

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const maxOutputBytes = 8192

type limitedOutput struct {
	buf      bytes.Buffer
	tooLarge bool
}

func (o *limitedOutput) Write(p []byte) (int, error) {
	remaining := maxOutputBytes - o.buf.Len()
	if len(p) > remaining {
		o.tooLarge = true
		o.buf.Write(p[:remaining])
	} else {
		o.buf.Write(p)
	}
	return len(p), nil
}

// Error is a failed credential source; a caller that tolerates other GitHub
// API failures must still surface it.
type Error struct{ text string }

func (e *Error) Error() string { return e.text }

func Parse(raw string) ([]string, error) {
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("token command must be a nonempty JSON argument array")
	}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return nil, fmt.Errorf("token command arguments must be nonempty single-line strings")
		}
	}
	return args, nil
}

func Run(args []string) (string, error) {
	if len(args) == 0 {
		return "", &Error{"token command is empty"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	var out limitedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", &Error{fmt.Sprintf("token command %q: %s", args[0], status(err))}
	}
	token := strings.TrimSuffix(out.buf.String(), "\n")
	token = strings.TrimSuffix(token, "\r")
	if out.tooLarge || token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", &Error{fmt.Sprintf("token command %q: invalid output (exit status 0)", args[0])}
	}
	return token, nil
}

func status(err error) string {
	if exit, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	return "start failed (no exit status)"
}
