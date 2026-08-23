package providervocab

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, pluginsRoot string, plugins ...string) {
	t.Helper()
	quoted := make([]string, len(plugins))
	for i, p := range plugins {
		quoted[i] = `"` + p + `"`
	}
	content := "schema_version = 1\ndescription = \"test catalog\"\nplugins = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(pluginsRoot, "catalog.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
	writeCatalog(t, root, "widget", "gadget")

	got, err := Collect(root)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []string{"gadget", "widget", "widget-runner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect = %v, want %v", got, want)
	}
}

func TestCollect_UnpublishedManifestErrors(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "widget", `
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "A widget plugin."
`)
	writePlugin(t, root, "unlisted", `
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "Present on disk but not published."
`)
	writeCatalog(t, root, "widget")

	if _, err := Collect(root); err == nil {
		t.Fatal("Collect: expected an error for a plugin.toml that catalog.toml does not list, got nil")
	}
}

func TestCollect_InvalidManifestErrors(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "broken", `schema_version = 2`)
	writeCatalog(t, root, "broken")

	if _, err := Collect(root); err == nil {
		t.Fatal("Collect: expected error for an unsupported schema_version, got nil")
	}
}
