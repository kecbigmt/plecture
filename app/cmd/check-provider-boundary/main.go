// Command check-provider-boundary prints the provider-name vocabulary
// scripts/check-provider-boundary.sh matches against: each shipped plugin's
// directory id and executable names, one per line. See
// app/internal/providervocab for why this is decoded rather than scanned as
// text.
package main

import (
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/app/internal/providervocab"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check-provider-boundary <plugins-root>")
		os.Exit(2)
	}
	words, err := providervocab.Collect(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, w := range words {
		fmt.Println(w)
	}
}
