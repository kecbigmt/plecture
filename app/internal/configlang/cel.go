package configlang

import (
	"fmt"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/ext"
)

// profileEnv is the Plecture CEL profile with no variables declared:
// standard syntax, operators, values, and the standard macros, plus the
// official CEL strings extension. Per-surface environments extend it with
// that surface's root identifiers.
//
// The extension's version is pinned rather than tracking cel-go, because a
// later version adds functions the profile does not document, and the
// function vocabulary a configuration may call has to be the one
// expressions.md enumerates.
var profileEnv = sync.OnceValues(func() (*cel.Env, error) {
	return cel.NewCustomEnv(
		cel.StdLib(),
		cel.Macros(cel.StandardMacros...),
		ext.Strings(ext.StringsVersion(profileStringsVersion)),
	)
})

// profileStringsVersion is the version of the CEL strings extension
// expressions.md admits.
const profileStringsVersion = 0

// env returns this surface's CEL environment: the profile plus one dynamic
// variable per root identifier the surface offers. Root granularity below
// the identifier is Plecture's own check, not CEL's, because CEL declares
// identifiers rather than paths.
func (s *Surface) env() (*cel.Env, error) {
	s.envOnce.Do(func() {
		base, err := profileEnv()
		if err != nil {
			s.envErr = err
			return
		}
		opts := make([]cel.EnvOption, 0, len(s.roots))
		for _, id := range s.identifiers() {
			opts = append(opts, cel.Variable(id, cel.DynType))
		}
		s.envValue, s.envErr = base.Extend(opts...)
	})
	return s.envValue, s.envErr
}

// checkExpression checks one CEL expression against the environment its
// surface declares. The checks run in the order the diagnostics table
// separates them, so the code reported for an expression that breaks several
// rules at once is the earliest layer that catches it.
func checkExpression(src string, s *Surface, pos Position) error {
	env, err := s.env()
	if err != nil {
		return err
	}
	parsed, issues := env.Parse(src)
	if issues != nil && issues.Err() != nil {
		return newDiag(CodeCELSyntax, LayerCEL, pos, oneLine(issues.Err()))
	}
	root := parsed.NativeRep().Expr()

	declared := make(map[string]bool, len(s.roots))
	for _, id := range s.identifiers() {
		declared[id] = true
	}
	if err := walkExpr(root, declared, map[string]bool{}, env, s, pos); err != nil {
		return err
	}
	if _, issues := env.Check(parsed); issues != nil && issues.Err() != nil {
		return newDiag(CodeCELType, LayerCEL, pos, oneLine(issues.Err()))
	}
	return nil
}

// walkExpr descends one expression, carrying the names a comprehension macro
// has bound so that an iteration variable is never mistaken for a surface
// root.
func walkExpr(e ast.Expr, declared, bound map[string]bool, env *cel.Env, s *Surface, pos Position) error {
	if e == nil {
		return nil
	}
	switch e.Kind() {
	case ast.LiteralKind, ast.UnspecifiedExprKind:
		return nil
	case ast.IdentKind:
		return checkReference(e.AsIdent(), declared, bound, s, pos)
	case ast.SelectKind:
		path, base, ok := flattenSelect(e)
		if !ok {
			return walkExpr(base, declared, bound, env, s, pos)
		}
		return checkReference(path, declared, bound, s, pos)
	case ast.CallKind:
		call := e.AsCall()
		if name := call.FunctionName(); !env.HasFunction(name) {
			return newDiag(CodeCELCustomFunction, LayerCEL, pos,
				fmt.Sprintf("%q is not a function the Plecture CEL profile defines", name))
		}
		if call.IsMemberFunction() {
			if err := walkExpr(call.Target(), declared, bound, env, s, pos); err != nil {
				return err
			}
		}
		for _, arg := range call.Args() {
			if err := walkExpr(arg, declared, bound, env, s, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.ListKind:
		for _, el := range e.AsList().Elements() {
			if err := walkExpr(el, declared, bound, env, s, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.MapKind:
		for _, entry := range e.AsMap().Entries() {
			kv := entry.AsMapEntry()
			if err := walkExpr(kv.Key(), declared, bound, env, s, pos); err != nil {
				return err
			}
			if err := walkExpr(kv.Value(), declared, bound, env, s, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.StructKind:
		for _, field := range e.AsStruct().Fields() {
			if err := walkExpr(field.AsStructField().Value(), declared, bound, env, s, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.ComprehensionKind:
		c := e.AsComprehension()
		if err := walkExpr(c.IterRange(), declared, bound, env, s, pos); err != nil {
			return err
		}
		inner := make(map[string]bool, len(bound)+3)
		for k := range bound {
			inner[k] = true
		}
		inner[c.IterVar()] = true
		if c.HasIterVar2() {
			inner[c.IterVar2()] = true
		}
		inner[c.AccuVar()] = true
		for _, sub := range []ast.Expr{c.AccuInit(), c.LoopCondition(), c.LoopStep(), c.Result()} {
			if err := walkExpr(sub, declared, inner, env, s, pos); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// checkReference splits the two questions a dotted reference raises: CEL
// declares identifiers, so the leading segment is CEL's to reject, and the
// rest of the path is the surface's.
func checkReference(path string, declared, bound map[string]bool, s *Surface, pos Position) error {
	head := path
	if i := strings.Index(path, "."); i >= 0 {
		head = path[:i]
	}
	if bound[head] {
		return nil
	}
	if !declared[head] {
		return newDiag(CodeCELUnknownName, LayerCEL, pos,
			fmt.Sprintf("%q is not a variable visible on the %s surface", head, s.Name))
	}
	if !s.offers(path) {
		return newDiag(s.rootCode, s.rootLayer, pos,
			fmt.Sprintf("%q is not a root the %s surface offers", path, s.Name))
	}
	return nil
}

// flattenSelect renders a select chain rooted at an identifier as a dotted
// path. A chain rooted at anything else — an indexed element, a function
// result — is not statically identifiable, so it reports the base for the
// walk to descend into instead.
func flattenSelect(e ast.Expr) (path string, base ast.Expr, ok bool) {
	var segments []string
	cur := e
	for cur.Kind() == ast.SelectKind {
		sel := cur.AsSelect()
		segments = append([]string{sel.FieldName()}, segments...)
		cur = sel.Operand()
	}
	if cur.Kind() != ast.IdentKind {
		return "", cur, false
	}
	return strings.Join(append([]string{cur.AsIdent()}, segments...), "."), nil, true
}

func oneLine(err error) string {
	return strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
}
