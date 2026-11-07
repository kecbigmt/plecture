package commands

import "os"

// invocationDir is the caller's working directory, whose trusted ancestors'
// `.plect/` overlays a direct invocation reads. An unreadable one selects no
// overlays, which is what a chain or a population sees.
func invocationDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
