package configlang

import (
	"fmt"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
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
	ref := func(path string) error {
		return checkReference(path, declared, s, pos)
	}
	if err := walkExpr(root, map[string]bool{}, env, ref, pos); err != nil {
		return err
	}
	if _, issues := env.Check(parsed); issues != nil && issues.Err() != nil {
		return newDiag(CodeCELType, LayerCEL, pos, oneLine(issues.Err()))
	}
	return nil
}

// walkExpr carries the names a comprehension macro has bound down the
// branches they cover, because that is the only place their scope is known.
func walkExpr(e ast.Expr, bound map[string]bool, env *cel.Env, ref referenceFunc, pos Position) error {
	if e == nil {
		return nil
	}
	switch e.Kind() {
	case ast.LiteralKind, ast.UnspecifiedExprKind:
		return nil
	case ast.IdentKind:
		return visitPath(e.AsIdent(), bound, ref)
	case ast.SelectKind:
		path, base, ok := flattenPath(e)
		if !ok {
			return walkExpr(base, bound, env, ref, pos)
		}
		return visitPath(path, bound, ref)
	case ast.CallKind:
		if path, _, ok := flattenPath(e); ok {
			return visitPath(path, bound, ref)
		}
		call := e.AsCall()
		if name := call.FunctionName(); !env.HasFunction(name) {
			return newDiag(CodeCELCustomFunction, LayerCEL, pos,
				fmt.Sprintf("%q is not a function the Plecture CEL profile defines", name))
		}
		if call.IsMemberFunction() {
			if err := walkExpr(call.Target(), bound, env, ref, pos); err != nil {
				return err
			}
		}
		for _, arg := range call.Args() {
			if err := walkExpr(arg, bound, env, ref, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.ListKind:
		for _, el := range e.AsList().Elements() {
			if err := walkExpr(el, bound, env, ref, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.MapKind:
		for _, entry := range e.AsMap().Entries() {
			kv := entry.AsMapEntry()
			if err := walkExpr(kv.Key(), bound, env, ref, pos); err != nil {
				return err
			}
			if err := walkExpr(kv.Value(), bound, env, ref, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.StructKind:
		for _, field := range e.AsStruct().Fields() {
			if err := walkExpr(field.AsStructField().Value(), bound, env, ref, pos); err != nil {
				return err
			}
		}
		return nil
	case ast.ComprehensionKind:
		c := e.AsComprehension()
		if err := walkExpr(c.IterRange(), bound, env, ref, pos); err != nil {
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
			if err := walkExpr(sub, inner, env, ref, pos); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

type referenceFunc func(path string) error

// visitPath drops a path rooted in a name a comprehension macro bound, so an
// iteration variable is never mistaken for a surface root.
func visitPath(path string, bound map[string]bool, ref referenceFunc) error {
	head, _, _ := strings.Cut(path, ".")
	if bound[head] {
		return nil
	}
	return ref(path)
}

// checkReference splits the two questions a dotted reference raises: CEL
// declares identifiers, so the leading segment is CEL's to reject, and the
// rest of the path is the surface's.
func checkReference(path string, declared map[string]bool, s *Surface, pos Position) error {
	head, _, _ := strings.Cut(path, ".")
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

// flattenPath folds a constant-key index into the dotted path because
// `state["revision"]` and `state.revision` name the same key, and both
// spellings have to reach the same root and the same contract. A chain rooted
// at a function result, or indexed by a computed key, names no path a
// contract could be checked against, so it reports the base for the walk to
// descend into and the enclosing root is what gets rejected.
func flattenPath(e ast.Expr) (path string, base ast.Expr, ok bool) {
	var segments []string
	cur := e
	for {
		switch cur.Kind() {
		case ast.IdentKind:
			return strings.Join(append([]string{cur.AsIdent()}, segments...), "."), nil, true
		case ast.SelectKind:
			sel := cur.AsSelect()
			segments = append([]string{sel.FieldName()}, segments...)
			cur = sel.Operand()
		case ast.CallKind:
			call := cur.AsCall()
			key, ok := constantIndexKey(call)
			if !ok {
				return "", cur, false
			}
			segments = append([]string{key}, segments...)
			cur = call.Args()[0]
		default:
			return "", cur, false
		}
	}
}

// constantIndexKey refuses a key carrying a dot: flattened, it would read as
// two segments, which is a different key.
func constantIndexKey(call ast.CallExpr) (string, bool) {
	if call.FunctionName() != operators.Index || len(call.Args()) != 2 {
		return "", false
	}
	arg := call.Args()[1]
	if arg.Kind() != ast.LiteralKind {
		return "", false
	}
	val := arg.AsLiteral()
	if val.Type() != types.StringType {
		return "", false
	}
	key, ok := val.Value().(string)
	if !ok || key == "" || strings.Contains(key, ".") {
		return "", false
	}
	return key, true
}

func oneLine(err error) string {
	return strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
}
