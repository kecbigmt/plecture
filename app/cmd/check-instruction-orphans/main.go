// Command check-instruction-orphans is scripts/check-instruction-orphans.sh's
// implementation: see instructionorphans.Check for what it does and why it
// parses TOML rather than scanning source text.
package main

import (
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/app/internal/instructionorphans"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: check-instruction-orphans <root>...")
		os.Exit(2)
	}
	orphans, err := instructionorphans.Check(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(orphans) > 0 {
		for _, o := range orphans {
			fmt.Printf("orphan: %s\n", o)
		}
		os.Exit(1)
	}
	fmt.Println("instruction-orphan check passed")
}
