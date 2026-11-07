package db

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
	"testing"
	"testing/fstest"
)

// generateManifest refuses a migration with a Down section instead of hashing it,
// so regenerating the manifest is never a way around the forward-only rule.
func generateManifest(migrations fs.FS) ([]byte, error) {
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

// verifyManifest reports every disagreement at once rather than the first, so one
// test run shows the whole set of files an edit touched.
func verifyManifest(migrations fs.FS, manifest []byte) error {
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
		"\nA migration that has shipped must never change. After an intended edit to an unreleased one, run `go test ./db -update` in app and commit migrations.sum with the change.")
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

func twoMigrations() fstest.MapFS {
	return fstest.MapFS{
		"20260101000000_first.sql":  {Data: []byte("-- +goose Up\nCREATE TABLE a (x INTEGER);\n")},
		"20260102000000_second.sql": {Data: []byte("-- +goose Up\nCREATE TABLE b (y INTEGER);\n")},
	}
}

// The expected hashes are what `sha256sum` prints for the same bytes, so
// the manifest stays checkable with `sha256sum -c` and no Go toolchain.
func TestManifest_ListsEverySQLFileSortedByNameInSha256sumFormat(t *testing.T) {
	fsys := twoMigrations()
	fsys["README.txt"] = &fstest.MapFile{Data: []byte("not a migration")}

	got, err := generateManifest(fsys)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	want := "" +
		"cb8ff0fd5fad6bdc73a467e5d649cc6d9cfc39f93206c255b197bdbcc6020a2b  20260101000000_first.sql\n" +
		"265ef801e1a69e316dd68b31b2aead9e3b0ee84d23327cc9ab629e8ab4e7dbfc  20260102000000_second.sql\n"
	if string(got) != want {
		t.Fatalf("generateManifest() =\n%s\nwant\n%s", got, want)
	}
}

func TestManifest_VerifyAcceptsAFreshlyGeneratedManifest(t *testing.T) {
	fsys := twoMigrations()
	manifest, err := generateManifest(fsys)
	if err != nil {
		t.Fatalf("generateManifest: %v", err)
	}
	if err := verifyManifest(fsys, manifest); err != nil {
		t.Fatalf("verify rejected a freshly generated manifest: %v", err)
	}
}

func TestManifest_VerifyRejectsADriftedManifest(t *testing.T) {
	cases := map[string]func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string){
		"a migration edited after the manifest was written": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			fsys["20260101000000_first.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE a (x INTEGER NOT NULL);\n")}
			return fsys, manifest
		},
		"a whitespace-only edit": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			fsys["20260101000000_first.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE a (x INTEGER);\n\n")}
			return fsys, manifest
		},
		"a migration the manifest does not list": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			fsys["20260103000000_third.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE c (z INTEGER);\n")}
			return fsys, manifest
		},
		"a listed migration that no longer exists": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			delete(fsys, "20260102000000_second.sql")
			return fsys, manifest
		},
		"a malformed manifest line": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			return fsys, manifest + "not a sum line\n"
		},
		"a duplicated manifest line": func(fsys fstest.MapFS, manifest string) (fstest.MapFS, string) {
			return fsys, manifest + strings.SplitAfter(manifest, "\n")[0]
		},
	}
	for name, drift := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := twoMigrations()
			manifest, err := generateManifest(fsys)
			if err != nil {
				t.Fatalf("generateManifest: %v", err)
			}
			fsys, drifted := drift(fsys, string(manifest))
			if err := verifyManifest(fsys, []byte(drifted)); err == nil {
				t.Fatal("verify accepted a manifest that no longer matches the migrations")
			}
		})
	}
}

func TestManifest_RejectsADownSection(t *testing.T) {
	annotations := []string{
		"-- +goose Down",
		"--+goose Down",
		"-- +goose down",
		"-- +goose DOWN  ",
	}
	for _, annotation := range annotations {
		t.Run(annotation, func(t *testing.T) {
			fsys := twoMigrations()
			fsys["20260102000000_second.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE b (y INTEGER);\n\n" + annotation + "\nDROP TABLE b;\n")}

			if _, err := generateManifest(fsys); err == nil || !strings.Contains(err.Error(), "20260102000000_second.sql") {
				t.Fatalf("generate error = %v, want a refusal naming the migration with a Down section", err)
			}

			clean := twoMigrations()
			manifest, err := generateManifest(clean)
			if err != nil {
				t.Fatalf("generateManifest: %v", err)
			}
			if err := verifyManifest(fsys, manifest); err == nil || !strings.Contains(err.Error(), "20260102000000_second.sql") {
				t.Fatalf("verify error = %v, want a refusal naming the migration with a Down section", err)
			}
		})
	}
}

func TestManifest_IgnoresTheWordDownInOrdinaryText(t *testing.T) {
	fsys := twoMigrations()
	fsys["20260102000000_second.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\n-- A capacity Down keeps the session; see the goose docs.\nCREATE TABLE b (y TEXT CHECK (y IN ('down', 'up')));\n")}
	if _, err := generateManifest(fsys); err != nil {
		t.Fatalf("generate rejected a migration with no Down section: %v", err)
	}
}
