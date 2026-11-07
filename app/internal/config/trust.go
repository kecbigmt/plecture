package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// AddTrustedProjectRoot records root in the trusted_project_roots of the
// config.toml at path, creating the file when it does not exist.
//
// The file is hand-authored, so only the key's own span is rewritten: a
// decode and re-encode would discard every comment and reorder the rest.
// The span comes from a parser rather than a text search, which would match
// the key inside a comment or a string.
func AddTrustedProjectRoot(path, root string) error {
	target, err := filepath.EvalSymlinks(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		line, err := renderTrustedRoots([]string{root})
		if err != nil {
			return err
		}
		body := fmt.Sprintf("schema_version = %d\n%s\n", lang.KnownSchemaVersion, line)
		return atomicfile.Write(path, []byte(body))
	case err != nil:
		return err
	}
	src, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	var current struct {
		TrustedProjectRoots []string `toml:"trusted_project_roots"`
	}
	if _, err := toml.Decode(string(src), &current); err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	if slices.Contains(current.TrustedProjectRoots, root) {
		return nil
	}
	start, end, err := trustedRootsSpan(src)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	line, err := renderTrustedRoots(append(current.TrustedProjectRoots, root))
	if err != nil {
		return err
	}
	if start == end {
		line += "\n"
		if start > 0 && src[start-1] != '\n' {
			line = "\n" + line
		}
	}
	var out bytes.Buffer
	out.Write(src[:start])
	out.WriteString(line)
	out.Write(src[end:])
	return atomicfile.Write(target, out.Bytes())
}

// trustedRootsSpan locates the bytes the key's assignment occupies. When the
// key is absent it returns an empty span at the start of the line after
// schema_version, which is necessarily top level: a key written after the
// first table header would belong to that table.
func trustedRootsSpan(src []byte) (int, int, error) {
	var p unstable.Parser
	p.Reset(src)
	insertAt := -1
	for p.NextExpression() {
		expr := p.Expression()
		if expr.Kind == unstable.Table || expr.Kind == unstable.ArrayTable {
			break
		}
		if expr.Kind != unstable.KeyValue {
			continue
		}
		key := expr.Key()
		key.Next()
		name := string(key.Node().Data)
		if key.Next() {
			continue
		}
		span := expr.Raw
		switch name {
		case "trusted_project_roots":
			return int(span.Offset), int(span.Offset + span.Length), nil
		case "schema_version":
			end := int(span.Offset + span.Length)
			if nl := bytes.IndexByte(src[end:], '\n'); nl >= 0 {
				insertAt = end + nl + 1
			} else {
				insertAt = len(src)
			}
		}
	}
	if err := p.Error(); err != nil {
		return 0, 0, err
	}
	if insertAt < 0 {
		return 0, 0, errors.New("schema_version is required")
	}
	return insertAt, insertAt, nil
}

// renderTrustedRoots encodes through the TOML encoder because a Go-quoted
// string is not always a valid TOML string.
func renderTrustedRoots(roots []string) (string, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(struct {
		Roots []string `toml:"trusted_project_roots"`
	}{roots}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
