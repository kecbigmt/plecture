package config

import (
	"fmt"
	"strings"
)

// TerminalConfig is a task's `[terminal]` table: the four raw terminal verbs
// a task that owns an interactive endpoint declares. Each member is a
// Go-template-rendered shell command string (single-line or multi-line TOML
// strings only — an array is a decode type error against these `string`
// fields, enforcing the ADR's "no arrays" rule for free).
//
// `attach` and `capture` receive no terminal operand; `send_text` and
// `send_keys` receive the literal text or key token as their first shell
// positional parameter ($1). Consumers reach these verbs through
// `plect attach` / `plect capture` (attach/capture only) or the
// `{{terminal "..."}}` template helper (all four) — never by naming the
// concrete multiplexer, so a task's own terminal implementation stays
// swappable behind the same four-verb shape.
type TerminalConfig struct {
	Attach   string `toml:"attach"`
	Capture  string `toml:"capture"`
	SendText string `toml:"send_text"`
	SendKeys string `toml:"send_keys"`
}

// Validate enforces the all-or-nothing rule: declaring any one member
// requires all four, because a partial table cannot honor every verb the
// {{terminal "..."}} helper promises to resolve. A nil receiver (no
// `[terminal]` table declared) is valid — most tasks own no interactive
// endpoint at all.
func (t *TerminalConfig) Validate() error {
	if t == nil {
		return nil
	}
	var missing []string
	if t.Attach == "" {
		missing = append(missing, "attach")
	}
	if t.Capture == "" {
		missing = append(missing, "capture")
	}
	if t.SendText == "" {
		missing = append(missing, "send_text")
	}
	if t.SendKeys == "" {
		missing = append(missing, "send_keys")
	}
	if len(missing) == 0 || len(missing) == 4 {
		// All set, or all empty (a bare `[terminal]` header with nothing
		// under it) — the latter carries no obligation, same as omitting
		// the table entirely.
		return nil
	}
	return fmt.Errorf("[terminal] table is missing required member(s): %s", strings.Join(missing, ", "))
}

// IsDeclared reports whether t carries an actual declaration (as opposed to
// a nil pointer, or a bare `[terminal]` header with every member left
// empty — see Validate).
func (t *TerminalConfig) IsDeclared() bool {
	return t != nil && (t.Attach != "" || t.Capture != "" || t.SendText != "" || t.SendKeys != "")
}
