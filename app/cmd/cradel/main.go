package main

import (
	"os"

	"github.com/cradel-dev/cradel/app/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
