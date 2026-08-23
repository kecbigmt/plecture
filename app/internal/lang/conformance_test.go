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
// conformance harness, and it asks one question of each artifact rather than
// the same question of both.
//
// For the definition language, this package's own validation is the authority:
// a fixture that declares a structural diagnostic must draw exactly that code
// out of ValidateDefinition. That is stronger than asking the schema, whose
// answer was only ever "some rule annotated with this code fired" — under a
// failing oneOf the validator blames every branch it tried, so the annotation
// was a proxy for the diagnostic rather than the diagnostic itself.
//
// plecture.schema.json is a derived artifact for editors, so what matters is
// that it does not reject configuration the language accepts. It is checked
// for exactly that and is not asked to name a diagnostic.
//
// The manifest and config-file entries have no definition to validate, so the
// schema remains their structural authority — consistent with those four
// schemas being hand-written rather than derived.
//
// The two assertions this cannot make from a registry and a fixture set alone
// — that the registry and the diagnostics chapter name the same codes, and
// that a chapter quotes its fixture verbatim — live in
// docs_conformance_test.go.
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
	// unreached names every fixture whose declared rule needs more than one
	// definition, or a plugin layer, to fire. Reported so the residue is a
	// number a reader can see rather than a silent gap.
	var unreached []string

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

			if exp.Entry != "definitions" && exp.Entry != "task" {
				// A manifest or config file declares no definition, so the
				// schema is what judges its shape.
				switch {
				case structuralMustFail && verr == nil:
					t.Errorf("expected the schema to reject this (%s), but it passed", exp.Diagnostic)
				case !structuralMustFail && verr != nil:
					t.Errorf("expected the schema to accept this (%s/%s), but it failed:\n    %s",
						exp.Result, exp.Layer, strings.ReplaceAll(verr.Error(), "\n", "\n    "))
				}
				return
			}

			got, gotErr := loaderDiagnosticOf(path, fixtureBody(string(raw)))
			if gotErr != nil {
				t.Fatalf("%v", gotErr)
			}
			if reachesLoader(exp) {
				switch got {
				case exp.Diagnostic:
					return
				case "":
					// A rule about more than one definition, or about what a
					// plugin layer declares, cannot fire on a fixture read on
					// its own. The claim stays unverified here rather than
					// being asserted against an environment the harness would
					// have to invent — but the fixture must still pass the
					// schema, which is what the check below does.
					unreached = append(unreached, filepath.ToSlash(rel))
				default:
					t.Errorf("the loader gave %s, want %s — a fixture must never draw a different diagnostic than it declares", got, exp.Diagnostic)
					return
				}
			} else if got != "" {
				t.Errorf("expected the language to accept this (%s/%s), but the loader gave %s",
					exp.Result, exp.Layer, got)
			}
			// The published schema must not contradict the language it
			// describes: a false rejection is what breaks an editor.
			if verr != nil && !structuralMustFail {
				t.Errorf("the language accepts this but plecture.schema.json rejects it:\n    %s",
					strings.ReplaceAll(verr.Error(), "\n", "\n    "))
			}
		})
	}

	for code := range documented {
		if !used[code] {
			t.Errorf("diagnostic %s is documented but no fixture exercises it", code)
		}
	}
	sort.Strings(unreached)
	t.Logf("%d fixtures checked; %d declare a rule a single-definition load cannot reach:\n  %s",
		checked, len(unreached), strings.Join(unreached, "\n  "))
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

// reachesLoader reports whether a fixture's declared failure is one loading a
// definition can reach. Loading covers the structural, semantic and CEL
// layers; `instantiation` is by definition a rule only a binding can break,
// and `accepted-invalid` records something the language admits on purpose.
func reachesLoader(exp fixtureExpectation) bool {
	if exp.Result != "invalid" {
		return false
	}
	switch exp.Layer {
	case "structural", "semantic", "cel":
		return true
	}
	return false
}

// loaderDiagnosticOf parses a fixture the way the loader does and returns the
// code of the first diagnostic its definitions draw, or "" when the language
// accepts them. A non-diagnostic error is the harness's own problem — a
// fixture it cannot read — and is reported as such.
func loaderDiagnosticOf(path, src string) (string, error) {
	var defs []*Definition
	var err error
	if strings.HasSuffix(path, ".md") {
		var one *Definition
		one, err = ParseTaskDocument(path, []byte(src))
		if one != nil {
			defs = []*Definition{one}
		}
	} else {
		defs, err = ParseDefinitionDocument(path, []byte(src))
	}
	if err != nil {
		var diag *Diagnostic
		if errors.As(err, &diag) {
			return string(diag.Code), nil
		}
		return "", fmt.Errorf("parse: %w", err)
	}
	validation := Validation{Executables: conformanceBins{}}
	for _, def := range defs {
		verr := validation.ValidateDefinition(def)
		if verr == nil {
			continue
		}
		var diag *Diagnostic
		if errors.As(verr, &diag) {
			return string(diag.Code), nil
		}
		return "", fmt.Errorf("validate %s: %w", def.ID, verr)
	}
	return "", nil
}

// conformanceBins accepts any executable name: whether a plugin declares one
// is a packaging question, and a fixture exercising the definition language
// should not have to stand up a catalog to ask a structural one.
type conformanceBins struct{}

func (conformanceBins) ResolveBin(ref string, from Ownership) (string, error) {
	return "/bin/" + ref, nil
}
