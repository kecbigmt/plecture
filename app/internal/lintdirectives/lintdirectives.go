// Package lintdirectives finds Go lint-exception comments that the
// repository's linters would honor but never report as stale.
//
// staticcheck never reports an unmatched U1000 ignore, and golangci-lint
// honors a //nolint that sits on its own line for the whole declaration or
// file below it, so an exception of either shape could outlive the code it
// excused without any check noticing.
package lintdirectives

import (
	"fmt"
	"go/scanner"
	"go/token"
	"regexp"
	"strings"
)

type Rule string

const (
	RuleStaticcheckDirective Rule = "staticcheck-directive"
	RuleStandaloneNolint     Rule = "standalone-nolint"
	RuleForeignNolint        Rule = "foreign-nolint"
	RuleNoncanonicalNolint   Rule = "noncanonical-nolint"
)

func (r Rule) Fix() string {
	switch r {
	case RuleStaticcheckDirective:
		return "U1000 takes no exception: delete the unused code, or add the build configuration that uses it to the matrix in scripts/check-go-unused.sh"
	case RuleStandaloneNolint:
		return "a //nolint on its own line excuses the whole declaration or file below it: move it to the end of the one line it excuses, as //nolint:errcheck // <reason>"
	case RuleNoncanonicalNolint:
		return "golangci-lint honors this spelling but nolintlint never checks it for a reason or staleness: write it exactly as //nolint:errcheck // <reason>"
	case RuleForeignNolint:
		return "errcheck is the only linter golangci-lint runs here, so a //nolint for any other linter, or for none, is never reported stale: name errcheck alone, or remove the directive"
	}
	return ""
}

type Finding struct {
	Path string
	Line int
	Rule Rule
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Rule, f.Rule.Fix())
}

// Check reports every rejected directive in one Go source file. It scans
// tokens rather than matching lines, so directive-shaped text inside a
// string literal is not mistaken for a comment.
func Check(path string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var errs scanner.ErrorList
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) { errs.Add(pos, msg) }, scanner.ScanComments)

	var findings []Finding
	lastCodeLine := 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		line := file.Line(pos)
		if tok != token.COMMENT {
			lastCodeLine = line
			continue
		}
		if rule, ok := classify(lit, line == lastCodeLine); ok {
			findings = append(findings, Finding{Path: path, Line: line, Rule: rule})
		}
	}
	if errs.Len() > 0 {
		return nil, errs.Err()
	}
	return findings, nil
}

// classify parses exactly as the linters do (staticcheck's exact //lint:
// prefix, golangci-lint's stripping of every leading slash and space): any
// narrower parse lets an honored directive escape this guard.
func classify(comment string, trailsCode bool) (Rule, bool) {
	if strings.HasPrefix(comment, "//lint:") {
		return RuleStaticcheckDirective, true
	}
	text := strings.TrimLeft(comment, "/ ")
	if !nolintPattern.MatchString(text) {
		return "", false
	}
	if !trailsCode {
		return RuleStandaloneNolint, true
	}
	if !namesOnlyErrcheck(text) {
		return RuleForeignNolint, true
	}
	if !canonicalNolint.MatchString(comment) {
		return RuleNoncanonicalNolint, true
	}
	return "", false
}

var (
	nolintPattern   = regexp.MustCompile(`^nolint( |:|$)`)
	canonicalNolint = regexp.MustCompile(`^//nolint:errcheck( |$)`)
)

func namesOnlyErrcheck(text string) bool {
	list, ok := strings.CutPrefix(text, "nolint:")
	if !ok {
		return false
	}
	list, _, _ = strings.Cut(list, "//")
	for _, name := range strings.Split(list, ",") {
		if strings.ToLower(strings.TrimSpace(name)) != "errcheck" {
			return false
		}
	}
	return true
}
