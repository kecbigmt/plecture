package service

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func TestSessionGuardForOwnSession(t *testing.T) {
	tests := []struct {
		name    string
		session string
		allowed []string
		denied  []string
	}{
		{
			name:    "scopes to self and its own subtree",
			session: "acme/widgets-758+claude",
			allowed: []string{
				"acme/widgets-758+claude",
				"acme/widgets-758+claude/child",
			},
			denied: []string{
				"acme/widgets-758+claude-other",
				"acme/widgets-759+claude",
				"acme/widgets-758",
				"other/widgets-758+claude",
			},
		},
		{
			name:    "quotes regex metacharacters literally",
			session: "owner/x+y",
			allowed: []string{"owner/x+y"},
			// Unescaped, "x+y" is "one or more x, then y" and would match this.
			denied: []string{"owner/xxy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard, err := SessionGuardForOwnSession(tt.session)
			if err != nil {
				t.Fatalf("SessionGuardForOwnSession(%q): %v", tt.session, err)
			}
			cfg := &config.Config{SessionGuard: guard}

			for _, name := range tt.allowed {
				ok, err := cfg.IsSessionNameAllowed(name)
				if err != nil {
					t.Fatalf("IsSessionNameAllowed(%q): %v", name, err)
				}
				if !ok {
					t.Errorf("guard %q: expected %q to be allowed", guard, name)
				}
			}
			for _, name := range tt.denied {
				ok, err := cfg.IsSessionNameAllowed(name)
				if err != nil {
					t.Fatalf("IsSessionNameAllowed(%q): %v", name, err)
				}
				if ok {
					t.Errorf("guard %q: expected %q to be denied", guard, name)
				}
			}
		})
	}
}

// TestSessionGuardForOwnSession_RejectsPathTraversal ensures a "../" name
// errors here rather than reaching defaultSessionMcpListenSocket's path.Join.
func TestSessionGuardForOwnSession_RejectsPathTraversal(t *testing.T) {
	for _, session := range []string{
		"../../etc/passwd",
		"owner/../../etc/passwd",
		"owner/..",
		"./owner",
		"",
	} {
		if _, err := SessionGuardForOwnSession(session); err == nil {
			t.Errorf("SessionGuardForOwnSession(%q): expected error, got nil", session)
		}
	}
}
