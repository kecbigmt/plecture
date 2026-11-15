package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultAppCachePathUsesPluginCacheDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLECT_CACHE_DIR", dir)
	if got, want := defaultAppCachePath(""), filepath.Join(dir, ".app-token-cache.json"); got != want {
		t.Fatalf("cache path = %q, want %q", got, want)
	}
}
