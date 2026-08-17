package commands

import (
	"bytes"
	"regexp"
	"testing"
)

func TestRemovedCommandsAreNotRegistered(t *testing.T) {
	for _, name := range []string{"bus", "watchdog", "workspace"} {
		if cmd, _, err := rootCmd.Find([]string{name}); err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("root command still registers %q", name)
		}
	}
}

func TestResidentDaemonCommandIsServe(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"serve"})
	if err != nil || cmd == rootCmd || cmd.Name() != "serve" {
		t.Fatalf("serve command is not registered")
	}

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := rootCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	help := out.String()
	if !regexp.MustCompile(`(?m)^\s+serve\s+Run the resident daemon$`).MatchString(help) {
		t.Fatalf("top-level help does not list serve:\n%s", help)
	}
	if regexp.MustCompile(`(?m)^\s+bus\s+`).MatchString(help) {
		t.Fatalf("top-level help still exposes bus vocabulary:\n%s", help)
	}
}
