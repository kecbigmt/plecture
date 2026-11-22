// Package lintdirectives finds Go comments that suppress a linter finding.
// The Go lint checks take no exception, so any such directive fails them.
package lintdirectives

import (
	"fmt"
	"go/scanner"
	"go/token"
	"regexp"
	"strings"
)

type Finding struct {
	Path string
	Line int
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: suppression directive: delete it and fix the finding it hides", f.Path, f.Line)
}

// Check scans tokens, not lines, so directive text in a string is no comment.
func Check(path string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var errs scanner.ErrorList
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) { errs.Add(pos, msg) }, scanner.ScanComments)

	var findings []Finding
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT && isDirective(lit) {
			findings = append(findings, Finding{Path: path, Line: file.Line(pos)})
		}
	}
	if errs.Len() > 0 {
		return nil, errs.Err()
	}
	return findings, nil
}

// isDirective parses exactly as the linters do (staticcheck's exact //lint:
// prefix, golangci-lint's stripping of every leading slash and space): any
// narrower parse lets an honored directive through.
func isDirective(comment string) bool {
	return strings.HasPrefix(comment, "//lint:") || nolintPattern.MatchString(strings.TrimLeft(comment, "/ "))
}

var nolintPattern = regexp.MustCompile(`^nolint( |:|$)`)
