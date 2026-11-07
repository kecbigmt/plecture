package commands

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
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
	if destroyCmd.Flags().Lookup("force") == nil {
		t.Fatal("destroy command missing \"force\" flag")
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

func TestDestroyRefusesTheRetiredInputFlagWithAPointerToTheMigrationGuide(t *testing.T) {
	t.Setenv("PLECT_CONFIG_HOME", t.TempDir())
	t.Setenv("PLECT_DATA_HOME", t.TempDir())
	t.Setenv("PLECT_SESSION_NAME", "")
	flag := destroyCmd.Flags().Lookup("input")
	if flag == nil {
		t.Fatal("destroy no longer recognizes --input, so its refusal cannot name the migration guide")
	}
	if err := flag.Value.Set("delete_branch=true"); err != nil {
		t.Fatal(err)
	}
	flag.Changed = true
	t.Cleanup(func() {
		flag.Changed = false
		_ = flag.Value.(interface{ Replace([]string) error }).Replace(nil)
	})
	err := destroyCmd.RunE(destroyCmd, []string{"session"})
	if err == nil || !strings.Contains(err.Error(), lang.WorkspaceProviderMigrationGuide) {
		t.Fatalf("destroy --input error = %v, want a refusal naming the migration guide", err)
	}
}
