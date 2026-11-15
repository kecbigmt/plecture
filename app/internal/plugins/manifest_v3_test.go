package plugins

import (
	"strings"
	"testing"
)

func TestManifestV3ServiceExecutableAndSchema(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `schema_version = 3
plect_min_version = "0.0.0"
[[executables]]
name = "worker"
path = "bin/worker"
service = true
[executables.config_schema]
type = "object"
required = ["region"]
[executables.config_schema.properties.region]
type = "string"
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Executables) != 1 || !m.Executables[0].Service || m.Executables[0].ConfigSchema == nil {
		t.Fatalf("executable = %+v", m.Executables)
	}
}

func TestManifestV3RejectsPluginServices(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `schema_version = 3
plect_min_version = "0.0.0"
[[services]]
name = "worker"
executable = "worker"
`)
	_, err := LoadManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "docs/migrations/") {
		t.Fatalf("error = %v", err)
	}
}
