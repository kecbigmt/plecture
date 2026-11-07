// Package migrationsum computes and verifies migrations.sum, the SHA-256
// manifest of the migration history in sha256sum format.
package migrationsum

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Generate refuses a migration with a Down section instead of hashing it,
// so regenerating the manifest is never a way around the forward-only rule.
func Generate(migrations fs.FS) ([]byte, error) {
	sums, err := hashMigrations(migrations)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)

	var b bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[name], name)
	}
	return b.Bytes(), nil
}

// Verify reports every disagreement at once rather than the first, so one
// test run shows the whole set of files an edit touched.
func Verify(migrations fs.FS, manifest []byte) error {
	actual, err := hashMigrations(migrations)
	if err != nil {
		return err
	}

	var problems []string
	listed := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	for line := 1; scanner.Scan(); line++ {
		sum, name, ok := strings.Cut(scanner.Text(), "  ")
		if !ok || len(sum) != sha256.Size*2 || strings.Trim(sum, "0123456789abcdef") != "" || name == "" {
			problems = append(problems, fmt.Sprintf("migrations.sum line %d is not \"<sha256 hex>  <file name>\": %q", line, scanner.Text()))
			continue
		}
		if _, dup := listed[name]; dup {
			problems = append(problems, fmt.Sprintf("migrations.sum lists %s more than once", name))
			continue
		}
		listed[name] = sum
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read migrations.sum: %w", err)
	}

	for name, want := range listed {
		got, ok := actual[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("migrations.sum lists %s, which does not exist", name))
		case got != want:
			problems = append(problems, fmt.Sprintf("%s does not match its hash in migrations.sum", name))
		}
	}
	for name := range actual {
		if _, ok := listed[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s is missing from migrations.sum", name))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n") +
		"\nA migration that has shipped must never change. After an intended edit to an unreleased one, run `go generate ./internal/persistence/...` in app and commit migrations.sum.")
}

func hashMigrations(migrations fs.FS) (map[string]string, error) {
	names, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sums := make(map[string]string, len(names))
	var downs []string
	for _, name := range names {
		content, err := fs.ReadFile(migrations, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if hasDownSection(content) {
			downs = append(downs, path.Base(name))
		}
		sum := sha256.Sum256(content)
		sums[name] = hex.EncodeToString(sum[:])
	}
	if len(downs) > 0 {
		return nil, fmt.Errorf("migrations are forward-only, but these have a `-- +goose Down` section: %s; delete the section, and fix a problem found after an upgrade with a new migration instead", strings.Join(downs, ", "))
	}
	return sums, nil
}

// hasDownSection recognizes an annotation line the way goose's own parser
// does — any comment line naming +goose, with the remaining word compared
// case-insensitively — so a spelling goose would run as Down cannot slip
// past a stricter literal match.
func hasDownSection(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") || !strings.Contains(line, "+goose") {
			continue
		}
		annotation := strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1)
		if strings.EqualFold(strings.TrimSpace(annotation), "Down") {
			return true
		}
	}
	return false
}
