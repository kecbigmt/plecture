package db

import (
	"strings"
	"testing"
	"testing/fstest"
)

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
