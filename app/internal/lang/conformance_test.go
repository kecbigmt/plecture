package lang

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestConformanceFixtures is the structural half of this package's
// conformance harness: it exercises every fixture under
// testdata/config-language/ against plecture.schema.json's seven entry
// anchors, the same assertions scripts/config-language-check made as a
// one-time specification-PR tool (see that script's doc comment). Running
// this in `go test` makes it a standing check: a schema or fixture edit that
// silently drifts from the documented diagnostic is exactly the invariant a
// future change could break without it. It does not re-implement
// scripts/config-language-check's docs/language/ worked-example
// byte-identity check, which is a docs-authoring concern rather than a
// loader one.
type fixtureExpectation struct {
	Result     string
	Layer      string
	Diagnostic string
	Entry      string
	Reason     string
}

var (
	fixtureHeaderRe   = regexp.MustCompile(`^(?:#|<!--)\s*plect-fixture:\s*(.*?)\s*(?:-->)?$`)
	fixtureReasonRe   = regexp.MustCompile(`^(?:#|<!--)\s*reason:\s*(.*?)\s*(?:-->)?$`)
	fixtureIsHeaderRe = regexp.MustCompile(`^(?:#|<!--)\s*(?:plect-fixture|reason):`)
)

// fixtureBody strips the expectation header (and the blank line after it),
// so a task document's frontmatter delimiter is the first thing seen — a
// real task document never carries this header at all.
func fixtureBody(src string) string {
	lines := strings.Split(src, "\n")
	i := 0
	for i < len(lines) && fixtureIsHeaderRe.MatchString(lines[i]) {
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

func parseFixtureHeader(src string) (fixtureExpectation, error) {
	var exp fixtureExpectation
	lines := strings.Split(src, "\n")
	m := fixtureHeaderRe.FindStringSubmatch(lines[0])
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
	if len(lines) > 1 {
		if r := fixtureReasonRe.FindStringSubmatch(lines[1]); r != nil {
			exp.Reason = r[1]
		}
	}
	if exp.Entry == "" {
		exp.Entry = "definitions"
	}
	return exp, nil
}

// fixtureFrontmatter splits a task document into its frontmatter, mirroring
// ParseTaskDocument's own delimiter handling but tolerant of a body that is
// not itself a load error (the conformance harness cares about the
// structural schema, not this package's own loader, for this pass).
func fixtureFrontmatter(src string) (string, error) {
	const delim = "+++\n"
	if !strings.HasPrefix(src, delim) {
		return "", fmt.Errorf("no frontmatter")
	}
	rest := src[len(delim):]
	end := strings.Index(rest, "\n"+delim)
	if end < 0 {
		return "", fmt.Errorf("unterminated frontmatter")
	}
	return rest[:end+1], nil
}

func normalizeForSchema(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
}

func TestConformanceFixtures(t *testing.T) {
	root := repoRoot(t)
	schemaPath := filepath.Join(root, "plecture.schema.json")
	fixtureRoot := filepath.Join(root, "testdata", "config-language")

	schemaFile, err := os.Open(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer schemaFile.Close()
	doc, err := jsonschema.UnmarshalJSON(schemaFile)
	if err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaPath, doc); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*jsonschema.Schema{}
	for _, anchor := range []string{"definitions", "task", "config", "catalogs", "lock", "plugin", "catalog"} {
		s, err := compiler.Compile(schemaPath + "#" + anchor)
		if err != nil {
			t.Fatalf("compile entry %q: %v", anchor, err)
		}
		entries[anchor] = s
	}

	documented := map[string]bool{}
	for _, c := range Codes() {
		documented[string(c)] = true
	}
	used := map[string]bool{}
	checked := 0

	var paths []string
	if err := filepath.Walk(fixtureRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "README.md" {
			return nil
		}
		if strings.HasSuffix(path, ".toml") || strings.HasSuffix(path, ".md") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("no fixtures found")
	}

	for _, path := range paths {
		rel, _ := filepath.Rel(fixtureRoot, path)
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			exp, err := parseFixtureHeader(string(raw))
			if err != nil {
				t.Fatalf("bad fixture header: %v", err)
			}
			checked++
			if exp.Diagnostic != "" {
				used[exp.Diagnostic] = true
				if !documented[exp.Diagnostic] {
					t.Errorf("diagnostic %s is not a documented code", exp.Diagnostic)
				}
			}
			schema, ok := entries[exp.Entry]
			if !ok {
				t.Fatalf("unknown schema entry %q", exp.Entry)
			}

			var decoded map[string]any
			var decodeErr error
			if strings.HasSuffix(path, ".md") {
				fm, err := fixtureFrontmatter(fixtureBody(string(raw)))
				if err != nil {
					decodeErr = err
				} else if _, err := toml.Decode(fm, &decoded); err != nil {
					decodeErr = fmt.Errorf("frontmatter does not parse as TOML: %w", err)
				}
			} else if _, err := toml.Decode(string(raw), &decoded); err != nil {
				decodeErr = fmt.Errorf("TOML does not parse: %w", err)
			}
			if decodeErr != nil {
				if exp.Result != "invalid" || exp.Layer != "structural" {
					t.Fatalf("decode: %v", decodeErr)
				}
				return
			}
			instance, err := normalizeForSchema(decoded)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			verr := schema.Validate(instance)

			structuralMustFail := exp.Result == "invalid" && exp.Layer == "structural"
			switch {
			case structuralMustFail && verr == nil:
				t.Errorf("expected the structural schema to reject this (%s), but it passed", exp.Diagnostic)
			case structuralMustFail:
				var ve *jsonschema.ValidationError
				if !errors.As(verr, &ve) {
					t.Errorf("rejected, but not with a validation error")
					break
				}
				if !schemaBlamesDiagnostic(doc, ve, exp.Diagnostic) {
					t.Errorf("rejected, but no rule annotated %s fired; rules that did:\n    %s",
						exp.Diagnostic, strings.Join(schemaBlamedRules(doc, ve), "\n    "))
				}
			case verr != nil:
				t.Errorf("expected the structural schema to accept this (%s/%s), but it failed:\n    %s",
					exp.Result, exp.Layer, strings.ReplaceAll(verr.Error(), "\n", "\n    "))
			}
		})
	}

	for code := range documented {
		if !used[code] {
			t.Errorf("diagnostic %s is documented but no fixture exercises it", code)
		}
	}
}

func schemaPointers(ve *jsonschema.ValidationError, out *[]string) {
	if _, frag, ok := strings.Cut(ve.SchemaURL, "#"); ok {
		*out = append(*out, frag)
	}
	for _, cause := range ve.Causes {
		schemaPointers(cause, out)
	}
}

func schemaAnnotationAt(doc any, pointer string) []string {
	var segments []string
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if seg != "" {
			segments = append(segments, strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~"))
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
		next, ok := schemaDescend(node, segments[i])
		if !ok {
			return out
		}
		node = next
	}
}

func schemaDescend(node any, segment string) (any, bool) {
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

func schemaBlamedRules(doc any, ve *jsonschema.ValidationError) []string {
	var ptrs []string
	schemaPointers(ve, &ptrs)
	seen := map[string]bool{}
	var out []string
	for _, p := range ptrs {
		for _, c := range schemaAnnotationAt(doc, p) {
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

func schemaBlamesDiagnostic(doc any, ve *jsonschema.ValidationError, code string) bool {
	for _, c := range schemaBlamedRules(doc, ve) {
		if strings.Contains(c, code) {
			return true
		}
	}
	return false
}
