package commands

import "testing"

func TestRemovedCommandsAreNotRegistered(t *testing.T) {
	for _, name := range []string{"watchdog", "workspace"} {
		if cmd, _, err := rootCmd.Find([]string{name}); err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("root command still registers %q", name)
		}
	}
}
