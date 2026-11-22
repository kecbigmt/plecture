// Command check-lint-directives is scripts/check-go-unused.sh's exception
// guard: see the lintdirectives package for what it rejects and why.
package main

import (
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/app/internal/lintdirectives"
)

func main() {
	failed := false
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		findings, err := lintdirectives.Check(path, src)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, f := range findings {
			fmt.Println(f)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Printf("lint-directive check passed (%d Go files scanned)\n", len(os.Args)-1)
}
