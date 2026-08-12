package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootCommandUsesCradelName(t *testing.T) {
	if rootCmd.Use != "cradel" {
		t.Fatalf("root command name = %q, want %q", rootCmd.Use, "cradel")
	}
}

func TestStageOneCommandPackagesUseCradelNames(t *testing.T) {
	paths := []string{
		filepath.Join("..", "cmd", "cradel", "main.go"),
		filepath.Join("..", "cmd", "cradel-web", "main.go"),
		filepath.Join("..", "..", "plugins", "github-provider", "cmd", "cradel-github-provider", "main.go"),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected Stage 1 command package at %s: %v", path, err)
		}
	}
}
