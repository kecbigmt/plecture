// Command migrationsum rewrites <dir>/migrations.sum from <dir>/migrations,
// where <dir> is its one argument.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrationsum <dir holding migrations/ and migrations.sum>")
		os.Exit(2)
	}
	dir := os.Args[1]
	manifest, err := generate(os.DirFS(filepath.Join(dir, "migrations")))
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrationsum:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrations.sum"), manifest, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "migrationsum:", err)
		os.Exit(1)
	}
}
