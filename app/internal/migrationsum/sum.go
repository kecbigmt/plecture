package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

func generate(migrations fs.FS) ([]byte, error) {
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

// verify reports every disagreement at once rather than the first, so one
// test run shows the whole set of files an edit touched.
func verify(migrations fs.FS, manifest []byte) error {
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
	for _, name := range names {
		content, err := fs.ReadFile(migrations, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(content)
		sums[name] = hex.EncodeToString(sum[:])
	}
	return sums, nil
}
