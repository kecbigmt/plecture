// config-language-check validates the config-language conformance fixtures
// against the hand-written plecture.schema.json, and checks that every
// worked example in docs/language/ is byte-identical to the fixture it names.
//
// It is a one-time verification tool for the specification PR, not a standing
// check: nothing wires it into CI. This module is deliberately not a go.work
// member, so it is run from its own directory with the workspace disabled;
// it locates the repository root itself:
//
//	cd scripts/config-language-check && GOWORK=off go run .
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaPath   = "plecture.schema.json"
	fixtureRoot  = "testdata/config-language"
	docsRoot     = "docs/language"
	diagnosticsD = "docs/language/overview.md"
)

// expectation is a fixture's declared outcome, parsed from its header.
type expectation struct {
	Result     string // valid | invalid | accepted-invalid
	Layer      string // structural | semantic | cel (absent for valid)
	Diagnostic string
	Entry      string // schema entry anchor
	Reason     string
}

var headerRe = regexp.MustCompile(`^#\s*plect-fixture:\s*(.*)$`)

func parseHeader(src string) (expectation, error) {
	var exp expectation
	lines := strings.Split(src, "\n")
	if len(lines) == 0 {
		return exp, fmt.Errorf("empty file")
	}
	m := headerRe.FindStringSubmatch(lines[0])
	if m == nil {
		return exp, fmt.Errorf("first line is not a plect-fixture header")
	}
	for _, field := range strings.Fields(m[1]) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			return exp, fmt.Errorf("header field %q is not key=value", field)
		}
		switch k {
		case "result":
			exp.Result = v
		case "layer":
			exp.Layer = v
		case "diagnostic":
			exp.Diagnostic = v
		case "entry":
			exp.Entry = v
		default:
			return exp, fmt.Errorf("unknown header field %q", k)
		}
	}
	if len(lines) > 1 && strings.HasPrefix(lines[1], "# reason:") {
		exp.Reason = strings.TrimSpace(strings.TrimPrefix(lines[1], "# reason:"))
	}
	if exp.Entry == "" {
		exp.Entry = "definitions"
	}
	switch exp.Result {
	case "valid":
		if exp.Layer != "" || exp.Diagnostic != "" {
			return exp, fmt.Errorf("a valid fixture declares no layer or diagnostic")
		}
	case "invalid", "accepted-invalid":
		if exp.Layer == "" || exp.Diagnostic == "" {
			return exp, fmt.Errorf("result=%s requires both layer and diagnostic", exp.Result)
		}
		switch exp.Layer {
		case "structural", "semantic", "cel":
		default:
			return exp, fmt.Errorf("unknown layer %q", exp.Layer)
		}
	default:
		return exp, fmt.Errorf("unknown result %q", exp.Result)
	}
	if exp.Reason == "" {
		return exp, fmt.Errorf("missing `# reason:` line")
	}
	return exp, nil
}

// body strips the expectation header so the remainder is what a docs example
// may quote verbatim.
func body(src string) string {
	lines := strings.Split(src, "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "# plect-fixture:") || i < len(lines) && strings.HasPrefix(lines[i], "# reason:") {
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

// normalize round-trips a TOML-decoded value through JSON so the validator
// sees the JSON type model (no int64, no time.Time).
func normalize(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
}

type report struct {
	failures []string
	checked  int
}

func (r *report) fail(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "config-language-check:", err)
		os.Exit(1)
	}
}

func run() error {
	// The paths below are repository-relative, so anchor the process there:
	// the tool lives in its own module and is therefore run from inside it.
	if err := chdirRepoRoot(); err != nil {
		return err
	}
	schemaDoc, err := os.Open(schemaPath)
	if err != nil {
		return err
	}
	defer schemaDoc.Close()
	doc, err := jsonschema.UnmarshalJSON(schemaDoc)
	if err != nil {
		return fmt.Errorf("parse %s: %w", schemaPath, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaPath, doc); err != nil {
		return err
	}

	entries := map[string]*jsonschema.Schema{}
	for _, anchor := range []string{"definitions", "config", "catalogs", "lock", "plugin", "catalog"} {
		s, err := compiler.Compile(schemaPath + "#" + anchor)
		if err != nil {
			return fmt.Errorf("compile entry %q: %w", anchor, err)
		}
		entries[anchor] = s
	}

	rep := &report{}
	documented, err := documentedDiagnostics()
	if err != nil {
		return err
	}
	usedDiagnostics := map[string]bool{}
	bodies := map[string]string{}

	var paths []string
	err = filepath.Walk(fixtureRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".toml") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		rel, _ := filepath.Rel(fixtureRoot, path)
		bodies[filepath.ToSlash(rel)] = body(src)

		exp, err := parseHeader(src)
		if err != nil {
			rep.fail("%s: bad header: %v", rel, err)
			continue
		}
		rep.checked++
		if exp.Diagnostic != "" {
			usedDiagnostics[exp.Diagnostic] = true
			if !documented[exp.Diagnostic] {
				rep.fail("%s: diagnostic %s is not listed in %s", rel, exp.Diagnostic, diagnosticsD)
			}
		}
		schema, ok := entries[exp.Entry]
		if !ok {
			rep.fail("%s: unknown schema entry %q", rel, exp.Entry)
			continue
		}

		var decoded map[string]any
		if _, err := toml.Decode(src, &decoded); err != nil {
			rep.fail("%s: TOML does not parse: %v", rel, err)
			continue
		}
		instance, err := normalize(decoded)
		if err != nil {
			rep.fail("%s: normalize: %v", rel, err)
			continue
		}
		verr := schema.Validate(instance)

		// A valid fixture, and one whose declared failure belongs to a later
		// validation layer, must both pass the structural schema: that is what
		// makes "the compiler rejects this" a meaningful claim rather than an
		// accident of shape.
		structuralMustFail := exp.Result == "invalid" && exp.Layer == "structural"
		switch {
		case structuralMustFail && verr == nil:
			rep.fail("%s: expected the structural schema to reject this (%s), but it passed", rel, exp.Diagnostic)
		case structuralMustFail:
			// Rejection alone is not evidence: the fixture must fail for the
			// reason it documents, so the rule that fired has to be one
			// annotated with that diagnostic. Under a failing `oneOf` the
			// validator blames every branch it tried, so this proves the
			// annotated rule fired — not that it was the only one to.
			var ve *jsonschema.ValidationError
			if !errors.As(verr, &ve) {
				rep.fail("%s: rejected, but not with a validation error", rel)
				break
			}
			if !blamesDiagnostic(doc, ve, exp.Diagnostic) {
				rep.fail("%s: rejected, but no rule annotated %s fired; rules that did:\n    %s",
					rel, exp.Diagnostic, indent(strings.Join(blamedRules(doc, ve), "\n")))
			}
		case verr != nil:
			rep.fail("%s: expected the structural schema to accept this (%s/%s), but it failed:\n    %s",
				rel, exp.Result, exp.Layer, indent(verr.Error()))
		}
	}

	for code := range documented {
		if !usedDiagnostics[code] {
			rep.fail("diagnostic %s is documented but no fixture exercises it", code)
		}
	}

	if err := checkDocExamples(bodies, rep); err != nil {
		return err
	}

	sort.Strings(rep.failures)
	for _, f := range rep.failures {
		fmt.Println("FAIL", f)
	}
	fmt.Printf("\n%d fixtures checked, %d diagnostics documented, %d failures\n",
		rep.checked, len(documented), len(rep.failures))
	if len(rep.failures) > 0 {
		os.Exit(1)
	}
	return nil
}

// chdirRepoRoot walks up from the working directory to the one holding the
// schema, so the tool can be invoked from its own module directory.
func chdirRepoRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, schemaPath)); err == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("%s not found in any parent of the working directory", schemaPath)
		}
		dir = parent
	}
}

// pointers collects the dereferenced schema locations every leaf of a
// validation error tree blames.
func pointers(ve *jsonschema.ValidationError, out *[]string) {
	if _, frag, ok := strings.Cut(ve.SchemaURL, "#"); ok {
		*out = append(*out, frag)
	}
	for _, cause := range ve.Causes {
		pointers(cause, out)
	}
}

// annotationAt reads the $comment of a schema location, and of each of its
// ancestors, so an annotation on a rule covers the keyword that enforces it.
func annotationAt(doc any, pointer string) []string {
	segments := []string{}
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if seg != "" {
			segments = append(segments, unescapePointer(seg))
		}
	}
	var out []string
	node := doc
	for i := 0; ; i++ {
		if m, ok := node.(map[string]any); ok {
			if c, ok := m["$comment"].(string); ok {
				out = append(out, c)
			}
		}
		if i >= len(segments) {
			return out
		}
		next, ok := descend(node, segments[i])
		if !ok {
			return out
		}
		node = next
	}
}

func descend(node any, segment string) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		v, ok := n[segment]
		return v, ok
	case []any:
		idx := 0
		if _, err := fmt.Sscanf(segment, "%d", &idx); err != nil || idx < 0 || idx >= len(n) {
			return nil, false
		}
		return n[idx], true
	}
	return nil, false
}

func unescapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

func blamedRules(doc any, ve *jsonschema.ValidationError) []string {
	var ptrs []string
	pointers(ve, &ptrs)
	seen := map[string]bool{}
	var out []string
	for _, p := range ptrs {
		for _, c := range annotationAt(doc, p) {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, "(no annotated rule on any blamed location)")
	}
	return out
}

func blamesDiagnostic(doc any, ve *jsonschema.ValidationError, code string) bool {
	for _, c := range blamedRules(doc, ve) {
		if strings.Contains(c, code) {
			return true
		}
	}
	return false
}

func indent(s string) string {
	return strings.ReplaceAll(s, "\n", "\n    ")
}

var diagnosticRe = regexp.MustCompile(`\bPLECT-CFG-[A-Z0-9-]+\b`)

// documentedDiagnostics reads the diagnostic codes listed in the overview's
// diagnostics table. Codes are the tooling interface, so every code a fixture
// claims must appear there and every listed code must have a fixture.
func documentedDiagnostics() (map[string]bool, error) {
	raw, err := os.ReadFile(diagnosticsD)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		for _, code := range diagnosticRe.FindAllString(line, -1) {
			out[code] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no diagnostic codes found in %s", diagnosticsD)
	}
	return out, nil
}

var docFixtureRe = regexp.MustCompile("(?m)^<!-- fixture: (\\S+) -->\\n```toml\\n((?s:.*?))```\\n")

// checkDocExamples enforces that a chapter's worked example is the fixture,
// not a paraphrase of it: prose that drifts from the executable specification
// is worse than no prose.
func checkDocExamples(bodies map[string]string, rep *report) error {
	docs, err := filepath.Glob(filepath.Join(docsRoot, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(docs)
	quoted := map[string]bool{}
	for _, doc := range docs {
		raw, err := os.ReadFile(doc)
		if err != nil {
			return err
		}
		for _, m := range docFixtureRe.FindAllStringSubmatch(string(raw), -1) {
			name, block := m[1], m[2]
			want, ok := bodies[name]
			if !ok {
				rep.fail("%s: names fixture %s, which does not exist", doc, name)
				continue
			}
			quoted[name] = true
			if strings.TrimRight(block, "\n") != strings.TrimRight(want, "\n") {
				rep.fail("%s: the example for %s is not verbatim", doc, name)
			}
		}
	}
	if len(quoted) == 0 {
		rep.fail("%s: no chapter quotes a fixture", docsRoot)
	}
	return nil
}
