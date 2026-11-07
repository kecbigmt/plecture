// Command migrationsum rewrites migrations.sum from the migrations/
// directory beside it. go generate runs it from the directory that holds
// both, so it takes no arguments.
package main

import (
	"fmt"
	"os"
)

func main() {
	manifest, err := generate(os.DirFS("migrations"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrationsum:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("migrations.sum", manifest, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "migrationsum:", err)
		os.Exit(1)
	}
}
