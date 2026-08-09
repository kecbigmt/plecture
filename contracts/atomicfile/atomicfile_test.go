package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := Write(path, []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
}

func TestWrite_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := Write(path, []byte("v1")); err != nil {
		t.Fatalf("Write v1: %v", err)
	}
	if err := Write(path, []byte("v2")); err != nil {
		t.Fatalf("Write v2: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("content = %q, want %q", got, "v2")
	}
}

func TestWrite_NoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := Write(path, []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.txt" {
		t.Fatalf("dir entries = %v, want only out.txt", entries)
	}
}

func TestWrite_MissingParentDirFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "out.txt")

	if err := Write(path, []byte("hello")); err == nil {
		t.Fatal("Write with missing parent dir: want error, got nil")
	}
}
