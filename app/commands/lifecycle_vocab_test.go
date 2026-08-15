package commands

import (
	"strings"
	"testing"
)

func TestLifecycleCommandsRemoveCreateOnly(t *testing.T) {
	if cmd, _, err := rootCmd.Find([]string{"create"}); err == nil && cmd != rootCmd {
		t.Fatal("create command is still registered")
	}

	for _, name := range []string{"up", "down", "destroy"} {
		if cmd, _, err := rootCmd.Find([]string{name}); err != nil || cmd == rootCmd {
			t.Fatalf("%q command is not registered", name)
		}
	}

	if downCmd.Flags().Lookup("rm") != nil {
		t.Fatal("down command unexpectedly exposes rm")
	}
	for _, name := range []string{"force", "input"} {
		if destroyCmd.Flags().Lookup(name) == nil {
			t.Fatalf("destroy command missing %q flag", name)
		}
	}
	if destroyCmd.Flags().Lookup("delete-branch") != nil {
		t.Fatal("destroy command must not expose delete-branch: core carries no branch vocabulary, branch deletion moved to provider cleanup")
	}
}

func TestRemovedLifecycleCommandHintForCreateOnly(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "create",
			args: []string{"create", "resource"},
			want: "Use `plect up <resource-id>` instead.",
		},
		{
			name: "destroy",
			args: []string{"destroy", "session"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removedLifecycleCommandHint(tt.args)
			if tt.want == "" {
				if got != "" {
					t.Fatalf("hint = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hint = %q, want %q", got, tt.want)
			}
		})
	}
}
