package main

import (
	"strings"
	"testing"
)

func TestServeRequiresSupervisedPrivateDirectories(t *testing.T) {
	t.Setenv("PLECT_DATA_DIR", "")
	t.Setenv("PLECT_CACHE_DIR", "")
	err := cmdServe(nil)
	if err == nil || !strings.Contains(err.Error(), "PLECT_DATA_DIR") {
		t.Fatalf("serve error = %v", err)
	}
}
