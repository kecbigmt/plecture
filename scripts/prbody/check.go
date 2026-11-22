package main

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
)

const (
	ruleClosingKeyword = "closing-keyword"
	ruleOverviewImage  = "overview-image-sha"
)

// Finding is one violation, at a 1-based line of the body.
type Finding struct {
	Line    int
	Rule    string
	Message string
}

// The keyword set and reference forms are the ones GitHub itself links, so a
// body this accepts is one GitHub records as closing the issue.
var closingRef = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+(?:#\d+|([\w.-]+/[\w.-]+)#\d+|https://github\.com/([\w.-]+/[\w.-]+)/issues/\d+)\b`)

var pinnedImage = regexp.MustCompile(`^https://raw\.githubusercontent\.com/([^/]+/[^/]+)/[0-9a-fA-F]{40}/[^?#]+$`)

var (
	allowMarker = regexp.MustCompile(`^<!--\s*check-pr-body:`)
	allowSyntax = regexp.MustCompile(`(?s)^<!--\s*check-pr-body:\s*allow\s+(\S+)\s+because\s+(\S.*?)\s*-->\s*$`)
)

// codeBreak replaces code spans and inline HTML in extracted text, so a
// keyword outside one cannot join a reference inside it.
const codeBreak = "\x00"

type allow struct {
	line int
	rule string
	used bool
}

type checker struct {
	source   []byte
	repo     string
	lines    []int
	allows   []*allow
	findings []Finding
}

// Check reports every rule violation in a pull request body for repo, given
// as owner/name.
func Check(body []byte, repo string) []Finding {
	c := &checker{source: body, repo: repo, lines: lineStarts(body)}
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(body))

	c.collectAllows(doc)
	if !c.hasClosingRef(doc) && !c.suppress(ruleClosingKeyword, 0) {
		c.add(1, ruleClosingKeyword, fmt.Sprintf(
			"the body names no issue of %s with a closing keyword, so GitHub never links this pull request to the issue it resolves. "+
				"Fix: add a line \"Closes #<issue>\" outside code (close/fix/resolve and their -s/-d forms also count). "+
				"Exception: <!-- check-pr-body: allow %s because <reason> -->",
			c.repo, ruleClosingKeyword))
	}
	c.checkOverviewImages(doc)
	for _, a := range c.allows {
		if !a.used {
			c.add(a.line, "stale-allow", fmt.Sprintf(
				"the allow for %s suppresses nothing. Fix: delete the comment.", a.rule))
		}
	}

	sort.SliceStable(c.findings, func(i, j int) bool { return c.findings[i].Line < c.findings[j].Line })
	return c.findings
}

func (c *checker) add(line int, rule, msg string) {
	c.findings = append(c.findings, Finding{Line: line, Rule: rule, Message: msg})
}

// suppress reports whether an allow covers rule; a line of 0 means the rule
// is about the whole body, so an allow anywhere covers it.
func (c *checker) suppress(rule string, line int) bool {
	for _, a := range c.allows {
		if a.rule == rule && (line == 0 || a.line == line) {
			a.used = true
			return true
		}
	}
	return false
}

func (c *checker) collectAllows(doc ast.Node) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.HTMLBlock:
			if n.HTMLBlockType == ast.HTMLBlockType2 {
				raw := blockText(n, c.source)
				if n.HasClosure() {
					raw += string(n.ClosureLine.Value(c.source))
				}
				c.parseAllow(raw, c.line(n.Lines().At(0).Start))
			}
		case *ast.RawHTML:
			if n.Segments.Len() > 0 {
				c.parseAllow(string(n.Segments.Value(c.source)), c.line(n.Segments.At(0).Start))
			}
		}
		return ast.WalkContinue, nil
	})
}

func (c *checker) parseAllow(raw string, line int) {
	raw = strings.TrimSpace(raw)
	if !allowMarker.MatchString(raw) {
		return
	}
	m := allowSyntax.FindStringSubmatch(raw)
	if m == nil || (m[1] != ruleClosingKeyword && m[1] != ruleOverviewImage) {
		c.add(line, "malformed-allow", fmt.Sprintf(
			"an allow must name %s or %s and give a reason. Fix: write <!-- check-pr-body: allow <rule> because <reason> -->.",
			ruleClosingKeyword, ruleOverviewImage))
		return
	}
	c.allows = append(c.allows, &allow{line: line, rule: m[1]})
}

func (c *checker) hasClosingRef(doc ast.Node) bool {
	found := false
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if n.Type() == ast.TypeBlock && n.FirstChild() != nil && n.FirstChild().Type() == ast.TypeInline {
			for _, m := range closingRef.FindAllStringSubmatch(inlineText(n, c.source), -1) {
				qualified := m[1] + m[2]
				if qualified == "" || strings.EqualFold(qualified, c.repo) {
					found = true
					return ast.WalkStop, nil
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// inlineText is the rendered prose of a block: code spans and inline HTML
// are what GitHub does not scan for references, so they become breaks.
func inlineText(n ast.Node, source []byte) string {
	var b strings.Builder
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch ch := ch.(type) {
			case *ast.Text:
				b.Write(ch.Segment.Value(source))
				if ch.SoftLineBreak() || ch.HardLineBreak() {
					b.WriteByte('\n')
				}
			case *ast.String:
				b.Write(ch.Value)
			case *ast.AutoLink:
				b.Write(ch.Label(source))
			case *ast.CodeSpan, *ast.RawHTML, *ast.Image:
				b.WriteString(codeBreak)
			default:
				visit(ch)
			}
		}
	}
	visit(n)
	return b.String()
}

func (c *checker) checkOverviewImages(doc ast.Node) {
	inOverview := false
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level <= 2 {
			inOverview = h.Level == 2 && strings.TrimSpace(inlineText(h, c.source)) == "Overview"
			continue
		}
		if !inOverview {
			continue
		}
		_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := n.(type) {
			case *ast.Image:
				c.checkImage(string(n.Destination), c.line(n.Pos()))
			case *ast.HTMLBlock:
				if n.HTMLBlockType != ast.HTMLBlockType2 && n.Lines().Len() > 0 {
					c.checkHTMLImages(blockText(n, c.source), n.Lines().At(0).Start)
				}
			case *ast.RawHTML:
				if n.Segments.Len() > 0 {
					c.checkHTMLImages(string(n.Segments.Value(c.source)), n.Segments.At(0).Start)
				}
			}
			return ast.WalkContinue, nil
		})
	}
}

func (c *checker) checkHTMLImages(raw string, start int) {
	z := html.NewTokenizer(strings.NewReader(raw))
	offset := start
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		tokenStart := offset
		offset += len(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if tok.Data != "img" {
			continue
		}
		for _, a := range tok.Attr {
			if a.Key == "src" {
				c.checkImage(a.Val, c.line(tokenStart))
			}
		}
	}
}

func (c *checker) checkImage(dest string, line int) {
	if m := pinnedImage.FindStringSubmatch(dest); m != nil && strings.EqualFold(m[1], c.repo) {
		return
	}
	if c.suppress(ruleOverviewImage, line) {
		return
	}
	c.add(line, ruleOverviewImage, fmt.Sprintf(
		"image %q under \"## Overview\" is not pinned to a commit of %s, so it shows whatever that ref holds later instead of the head under review. "+
			"Fix: commit the image and embed it as https://raw.githubusercontent.com/%s/<full 40-hex commit sha>/<path>. "+
			"Exception: <!-- check-pr-body: allow %s because <reason> --> on the same line",
		dest, c.repo, c.repo, ruleOverviewImage))
}

func blockText(n ast.Node, source []byte) string {
	var b bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
	return b.String()
}

func lineStarts(source []byte) []int {
	starts := []int{0}
	for i, ch := range source {
		if ch == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (c *checker) line(offset int) int {
	if offset < 0 {
		return 1
	}
	return sort.Search(len(c.lines), func(i int) bool { return c.lines[i] > offset })
}
