package configlang

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LeafValue resolves one leaf of a `{ json = ... }` operand: a literal, a
// projection by path, or a computation by source. It reports absence
// separately from error, because `optional = true` propagates absence rather
// than substituting a sentinel.
type LeafValue func(v *Value) (value any, absent bool, err error)

// RenderJSON serializes one `{ json = ... }` operand. Two serializations of
// the same operand and the same leaf values are byte-identical: object keys
// are ordered, so a payload can be compared, cached, and signed. absent is
// true when the whole operand resolved to nothing.
func RenderJSON(op *JSONOperand, leaf LeafValue) (data []byte, absent bool, err error) {
	tree, absent, err := resolveOperand(op, leaf)
	if err != nil || absent {
		return nil, absent, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// A Plecture payload crosses a process boundary, not an HTML one, and
	// escaping it would change the bytes a receiving executable parses.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tree); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), false, nil
}

// resolveOperand walks the operand into a plain value tree. json.Marshal
// orders a map's keys, so the tree it produces serializes identically every
// time.
func resolveOperand(op *JSONOperand, leaf LeafValue) (any, bool, error) {
	switch {
	case op == nil:
		return nil, true, nil
	case op.Leaf != nil:
		return leaf(op.Leaf)
	case op.Object != nil:
		out := make(map[string]any, len(op.Object))
		for key, child := range op.Object {
			value, absent, err := resolveOperand(child, leaf)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", key, err)
			}
			if absent {
				continue
			}
			out[key] = value
		}
		return out, false, nil
	default:
		out := make([]any, 0, len(op.Array))
		for i, child := range op.Array {
			value, absent, err := resolveOperand(child, leaf)
			if err != nil {
				return nil, false, fmt.Errorf("[%d]: %w", i, err)
			}
			if absent {
				continue
			}
			out = append(out, value)
		}
		return out, false, nil
	}
}
