package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

func TestBuildOutputPaths(t *testing.T) {
	m := Manifest{Executables: []Executable{
		{Name: "script", Path: "bin/script"},
		{Name: "built", Path: "bin/built", Build: "go build -o bin/built ./cmd/built"},
	}}
	got := BuildOutputPaths(m)
	if len(got) != 1 || got[0] != "bin/built" {
		t.Fatalf("BuildOutputPaths = %v, want [\"bin/built\"]", got)
	}
}

func TestRunBuilds_SkipsBuildLessEntries(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Executables: []Executable{{Name: "script", Path: "bin/script"}}}

	// No build command declared, so RunBuilds must not require bin/script to
	// exist or run anything.
	if err := RunBuilds(context.Background(), procexec.Default, dir, m); err != nil {
		t.Fatalf("RunBuilds: unexpected error: %v", err)
	}
}

func TestRunBuilds_RunsBuildAndVerifiesOutput(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Executables: []Executable{{
		Name:  "built",
		Path:  "bin/built",
		Build: "mkdir -p bin && printf '#!/bin/sh\\necho hi\\n' > bin/built && chmod +x bin/built",
	}}}

	if err := RunBuilds(context.Background(), procexec.Default, dir, m); err != nil {
		t.Fatalf("RunBuilds: unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "built")); err != nil {
		t.Fatalf("build output not found: %v", err)
	}
}

func TestRunBuilds_FailsWhenCommandFails(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Executables: []Executable{{Name: "built", Path: "bin/built", Build: "exit 1"}}}

	if err := RunBuilds(context.Background(), procexec.Default, dir, m); err == nil {
		t.Fatal("RunBuilds: want error when the build command fails, got nil")
	}
}

func TestRunBuilds_FailsWhenOutputMissing(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Executables: []Executable{{Name: "built", Path: "bin/built", Build: "true"}}}

	if err := RunBuilds(context.Background(), procexec.Default, dir, m); err == nil {
		t.Fatal("RunBuilds: want error when the build command does not produce the declared path, got nil")
	}
}
