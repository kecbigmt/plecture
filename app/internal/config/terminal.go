package config

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// TerminalConfig is an effect's `[terminal]` table: the terminal verbs an
// effect that owns an interactive endpoint declares, each an action.
//
// `attach` and `capture` receive no operand; `send_text` and `send_keys`
// receive the literal text or key token as the action's first positional
// argument. Consumers reach these verbs through `plect attach` /
// `plect capture` or through a `{ terminal = "<verb>" }` value — never by
// naming the concrete multiplexer, so an effect's own terminal
// implementation stays swappable behind the same verb vocabulary.
//
// A partial table is legal: availability is per verb, so an effect may offer
// a capture and nothing else, and a value consuming a verb no effect in the
// plan declares fails where it is consumed.
type TerminalConfig struct {
	Attach   *lang.Action
	Capture  *lang.Action
	SendText *lang.Action
	SendKeys *lang.Action
}

// Verb returns the action one verb name selects. An undeclared verb is an
// error rather than a nil action, so a consumer never resolves to nothing.
func (t *TerminalConfig) Verb(name string) (*lang.Action, error) {
	var action *lang.Action
	if t != nil {
		switch name {
		case "attach":
			action = t.Attach
		case "capture":
			action = t.Capture
		case "send_text":
			action = t.SendText
		case "send_keys":
			action = t.SendKeys
		default:
			return nil, fmt.Errorf("unknown terminal verb %q (want attach, capture, send_text, or send_keys)", name)
		}
	}
	if action == nil {
		return nil, fmt.Errorf("no effect in this workflow's plan declares the terminal verb %q", name)
	}
	return action, nil
}

// IsDeclared reports whether t carries an actual declaration (as opposed to
// a nil pointer, or a bare `[terminal]` header with every member left
// empty).
func (t *TerminalConfig) IsDeclared() bool {
	return t != nil && (t.Attach != nil || t.Capture != nil || t.SendText != nil || t.SendKeys != nil)
}
