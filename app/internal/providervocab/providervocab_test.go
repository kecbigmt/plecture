package providervocab

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writePlugin(t *testing.T, pluginsRoot, id, content string) {
	t.Helper()
	dir := filepath.Join(pluginsRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect_IdAndExecutableNames(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "widget", `
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "A widget plugin."

[[executables]]
name = "widget-runner"
path = "bin/widget-runner"
`)
	writePlugin(t, root, "gadget", `
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "A gadget plugin."
`)

	got, err := Collect(root)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []string{"gadget", "widget", "widget-runner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect = %v, want %v", got, want)
	}
}

func TestCollect_SkipsDirectoryWithoutManifest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "widget", `
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "A widget plugin."
`)
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Collect(root)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []string{"widget"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect = %v, want %v (not-a-plugin must not appear)", got, want)
	}
}

func TestCollect_InvalidManifestErrors(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "broken", `schema_version = 2`)

	if _, err := Collect(root); err == nil {
		t.Fatal("Collect: expected error for an unsupported schema_version, got nil")
	}
}
