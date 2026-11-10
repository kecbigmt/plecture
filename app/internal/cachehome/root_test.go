package cachehome

import (
	"path/filepath"
	"testing"
)

func TestResolveReturnsCacheRoot(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Setenv(XDGEnvVar, t.TempDir())
	root, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "plect" {
		t.Fatalf("cache root = %q", root)
	}
}
