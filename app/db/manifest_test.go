package db

import (
	"io/fs"
	"os"
	"testing"

	"github.com/kecbigmt/plecture/app/db/migrationsum"
)

func TestMigrationsMatchTheirManifest(t *testing.T) {
	manifest, err := os.ReadFile("migrations.sum")
	if err != nil {
		t.Fatalf("read migrations.sum: %v", err)
	}
	migrations, err := fs.Sub(Migrations, "migrations")
	if err != nil {
		t.Fatalf("migrations subtree: %v", err)
	}
	if err := migrationsum.Verify(migrations, manifest); err != nil {
		t.Fatal(err)
	}
}
