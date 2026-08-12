package main

import (
	"os"

	"github.com/plecture/plect/app/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
