package main

import (
	"os"

	"github.com/kecbigmt/sennit/app/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
