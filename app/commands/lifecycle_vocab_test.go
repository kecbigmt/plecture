package commands

import (
	"strings"
	"testing"
)

func TestLifecycleCommandsExposeOnlyUpAndDown(t *testing.T) {
	for _, removed := range []string{"create", "destroy"} {
		if cmd, _, err := rootCmd.Find([]string{removed}); err == nil && cmd != rootCmd {
			t.Fatalf("%q command is still registered", removed)
		}
	}

	for _, name := range []string{"rm", "force", "delete-branch"} {
		if downCmd.Flags().Lookup(name) == nil {
			t.Fatalf("down command missing %q flag", name)
		}
	}
}

func TestRemovedLifecycleCommandHints(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "create",
			args: []string{"create", "resource"},
			want: "Use `sennit up <resource-id>` instead.",
		},
		{
			name: "destroy",
			args: []string{"destroy", "session"},
			want: "Use `sennit down <resource-id|session> --rm` instead.",
		},
		{
			name: "destroy force",
			args: []string{"destroy", "session", "--force"},
			want: "Use `sennit down <resource-id|session> --rm --force` instead.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removedLifecycleCommandHint(tt.args)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("hint = %q, want %q", got, tt.want)
			}
		})
	}
}
