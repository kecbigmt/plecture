package config

import "github.com/kecbigmt/plecture/app/internal/lang"

// The three constructors below state a value the way a declaration does, so a
// fixture reads as the wiring it stands for rather than as a struct literal.

func literalValue(v string) *lang.Value { return &lang.Value{Form: lang.FormLiteral, Literal: v} }

func fromValue(path string) *lang.Value { return &lang.Value{Form: lang.FormFrom, From: path} }

func fromValueOr(path, fallback string) *lang.Value {
	return &lang.Value{Form: lang.FormFrom, From: path, Default: fallback, HasDefault: true}
}
