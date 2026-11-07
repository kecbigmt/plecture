package db

import (
	"flag"
	"io/fs"
	"os"
	"testing"
)

// The author of a migration change rewrites the manifest with -update and
// commits it with the change; every other run, CI's included, only verifies.
var update = flag.Bool("update", false, "rewrite migrations.sum from migrations/")

func TestMigrationsMatchTheirManifest(t *testing.T) {
	migrations, err := fs.Sub(Migrations, "migrations")
	if err != nil {
		t.Fatalf("migrations subtree: %v", err)
	}
	if *update {
		manifest, err := generateManifest(migrations)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("migrations.sum", manifest, 0o644); err != nil {
			t.Fatalf("write migrations.sum: %v", err)
		}
		return
	}
	manifest, err := os.ReadFile("migrations.sum")
	if err != nil {
		t.Fatalf("read migrations.sum: %v", err)
	}
	if err := verifyManifest(migrations, manifest); err != nil {
		t.Fatal(err)
	}
}
