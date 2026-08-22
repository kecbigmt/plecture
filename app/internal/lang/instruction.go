package lang

import (
	"fmt"
	"strings"
)

// RenderInstruction resolves the projections in a task document's body. A
// `{{ <path> }}` in prose is the `from` projection in prose position — the
// same root vocabulary, already checked at load against the roots this
// surface declares — so it resolves the same way, and stringifies, because
// prose has nowhere to put a list.
func RenderInstruction(body string, env Environment) (string, error) {
	eval := Eval{Env: env}
	var err error
	rendered := bodyProjection.ReplaceAllStringFunc(body, func(match string) string {
		if err != nil {
			return ""
		}
		path := strings.TrimSpace(strings.Trim(match, "{}"))
		resolved, _, verr := eval.Value(&Value{Form: FormFrom, From: path})
		if verr != nil {
			err = fmt.Errorf("instruction: %w", verr)
			return ""
		}
		s, serr := stringify(resolved)
		if serr != nil {
			err = fmt.Errorf("instruction: %q: %w", path, serr)
			return ""
		}
		return s
	})
	if err != nil {
		return "", err
	}
	return rendered, nil
}
